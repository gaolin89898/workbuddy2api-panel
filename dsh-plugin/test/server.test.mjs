import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createHandler, localRequest, PREFIX } from '../lib/server.js';
import { apply, keySyncer } from '../lib/index.js';

async function serve(t, handler) {
  const server = http.createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  return { port: server.address().port, url: `http://127.0.0.1:${server.address().port}` };
}

test('local bridge rejects remote peers, DNS rebinding, cross-origin and forwarded requests', () => {
  const request = (headers = {}, address = '127.0.0.1') => ({ socket: { remoteAddress: address }, headers: { host: '127.0.0.1:3080', ...headers } });
  assert.equal(localRequest(request()), true);
  assert.equal(localRequest(request({ origin: 'http://127.0.0.1:3080' })), true);
  for (const headers of [
    { host: 'evil.example:3080' }, { origin: 'http://evil.example' },
    { origin: 'http://127.0.0.1:5174' }, { origin: 'null' },
    { 'sec-fetch-site': 'cross-site' }, { forwarded: 'for=1.2.3.4' },
    { 'x-forwarded-for': '127.0.0.1' }, { host: 'evil@127.0.0.1:3080' },
  ]) assert.equal(localRequest(request(headers)), false, JSON.stringify(headers));
  assert.equal(localRequest(request({}, '192.168.1.9')), false);
  assert.throws(() => createHandler({ backendPort: 'http://evil.example' }));
});

test('forwards the supplied Bearer key and JSON to the fixed local API only', async t => {
  const requests = [];
  const backend = await serve(t, (req, res) => {
    requests.push({ url: req.url, headers: req.headers, method: req.method });
    res.writeHead(200, { 'content-type': 'application/json' });
    req.pipe(res);
  });
  const ui = await serve(t, createHandler({ backendPort: backend.port }));
  const response = await fetch(ui.url + PREFIX + 'api/accounts/test/tasks/auto?check=1', {
    method: 'POST', headers: { Authorization: 'Bearer unit-test', 'content-type': 'application/json', Cookie: 'dsh_secret=private', Origin: ui.url },
    body: JSON.stringify({ task_code: 'test' }),
  });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { task_code: 'test' });
  assert.equal(requests[0].url, '/panel/api/accounts/test/tasks/auto?check=1');
  assert.equal(requests[0].headers.authorization, 'Bearer unit-test');
  assert.equal(requests[0].headers.cookie, undefined);
  assert.equal(requests[0].headers.origin, undefined);
  assert.equal(requests[0].headers['x-forwarded-for'], undefined);
  const unauthenticated = await fetch(ui.url + PREFIX + 'api/overview');
  assert.equal(unauthenticated.status, 401);
  const crossSite = await fetch(ui.url + PREFIX + 'api/overview', { headers: { Authorization: 'Bearer unit-test', Origin: 'http://evil.example' } });
  assert.equal(crossSite.status, 403);
  const traversal = await fetch(ui.url + PREFIX + 'api/%2e%2e%2fhealthz', { headers: { Authorization: 'Bearer unit-test' } });
  assert.equal(traversal.status, 400);
  const secret = await fetch(ui.url + PREFIX + 'config.json');
  assert.equal(secret.status, 404);
  assert.equal(requests.length, 1);
});

test('preserves authentication errors and never follows backend redirects', async t => {
  let calls = 0;
  const backend = await serve(t, (req, res) => {
    calls++;
    if (req.url.endsWith('/redirect')) { res.writeHead(302, { location: '/secret' }); res.end(); return; }
    res.writeHead(401, { 'content-type': 'application/json' });
    res.end('{"error":"invalid_api_key"}');
  });
  const ui = await serve(t, createHandler({ backendPort: backend.port }));
  const headers = { Authorization: 'Bearer invalid' };
  const denied = await fetch(ui.url + PREFIX + 'api/overview', { headers });
  assert.equal(denied.status, 401);
  assert.equal((await denied.json()).error, 'invalid_api_key');
  const redirect = await fetch(ui.url + PREFIX + 'api/redirect', { headers });
  assert.equal(redirect.status, 502);
  assert.equal(calls, 2);
});

test('bootstraps without credentials and syncs the actual key after automatic connection', async t => {
  const synced = [];
  const backend = await serve(t, (req, res) => {
    if (req.url === '/panel/api/auth/bootstrap') {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ api_key: 'actual-key', key_available: true }));
      return;
    }
    assert.equal(req.headers.authorization, 'Bearer actual-key');
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end('{}');
  });
  const ui = await serve(t, createHandler({ backendPort: backend.port }, {
    onApiKey: async key => { synced.push(key); },
  }));
  const bootstrap = await fetch(ui.url + PREFIX + 'api/auth/bootstrap');
  assert.equal(bootstrap.status, 200);
  const { api_key } = await bootstrap.json();
  // 过期会话不应经公开引导接口写入网关凭证库。
  await fetch(ui.url + PREFIX + 'api/auth/bootstrap', { headers: { Authorization: 'Bearer old-session' } });
  assert.deepEqual(synced, []);
  const connected = await fetch(ui.url + PREFIX + 'api/overview', { headers: { Authorization: `Bearer ${api_key}` } });
  assert.equal(connected.status, 200);
  assert.deepEqual(synced, ['actual-key']);
});

test('plugin registers one disposable route without starting the backend', () => {
  let registered, disposed = false, cleanup;
  apply({
    webServer: { register(route) { registered = route; return () => { disposed = true; }; } },
    credentials: { resolve: async () => undefined, set: async () => {} },
    effect(fn) { cleanup = fn(); },
  });
  assert.equal(registered.path, PREFIX.slice(0, -1));
  assert.equal(typeof registered.handler, 'function');
  cleanup();
  assert.equal(disposed, true);
});

test('accepted keys sync into the DSH credential store once per value', async t => {
  const stored = new Map();
  let resolveCalls = 0;
  const credentials = {
    async resolve(ref) {
      resolveCalls++;
      assert.equal(ref, 'WB2API_GATEWAY_API_KEY');
      return stored.has(ref) ? { value: stored.get(ref), source: 'file' } : undefined;
    },
    async set(ref, value) { stored.set(ref, value); },
  };
  const backend = await serve(t, (req, res) => {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end('{}');
  });
  const ui = await serve(t, createHandler({ backendPort: backend.port }, { onApiKey: keySyncer(credentials) }));
  const call = async key => {
    await fetch(ui.url + PREFIX + 'api/overview', {
      headers: { Authorization: `Bearer ${key}`, Origin: ui.url },
    });
    // 同步是 fire-and-forget：等待一轮宏任务让它落定。
    await new Promise(resolve => setTimeout(resolve, 20));
  };
  await call('key-1');
  assert.equal(stored.get('WB2API_GATEWAY_API_KEY'), 'key-1');
  await call('key-1');
  const afterFirst = resolveCalls;
  await call('key-2');
  assert.equal(stored.get('WB2API_GATEWAY_API_KEY'), 'key-2');
  assert.equal(resolveCalls, afterFirst + 1); // 同值整体跳过，新值解析后写入
});
