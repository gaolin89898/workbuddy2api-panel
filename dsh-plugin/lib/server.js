import { readFile } from 'node:fs/promises';

export const PREFIX = '/workbuddy-accounts/';
const assets = new Map([
  ['index.html', 'text/html; charset=utf-8'],
  ['accounts.js', 'text/javascript; charset=utf-8'],
  ['index.css', 'text/css; charset=utf-8'],
]);

function loopback(host) {
  return host === 'localhost' || host === '::1' || host === '127.0.0.1' || host === '::ffff:127.0.0.1';
}

// The bridge grants no remote access to desktop operations. In addition to
// the backend's Bearer authentication, require a local peer and same-origin UI.
export function localRequest(req) {
  if (!loopback(req.socket.remoteAddress)) return false;
  if (['forwarded', 'x-forwarded-for', 'x-forwarded-host', 'x-real-ip'].some(h => req.headers[h])) return false;
  if (req.headers['sec-fetch-site'] === 'cross-site') return false;
  try {
    const host = new URL('http://' + req.headers.host);
    if (!loopback(host.hostname.replace(/^\[|\]$/g, ''))) return false;
    if (host.username || host.password || host.pathname !== '/' || host.search || host.hash) return false;
    if (req.headers.origin) {
      const origin = new URL(req.headers.origin);
      if (!['http:', 'https:'].includes(origin.protocol) || origin.origin !== req.headers.origin || origin.host !== host.host) return false;
    }
    return true;
  } catch { return false; }
}

function json(res, status, error) {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' });
  res.end(JSON.stringify({ error }));
}

async function readBody(req, res, limit) {
  const chunks = [];
  let size = 0;
  try {
    for await (const chunk of req) {
      size += chunk.length;
      if (size > limit) { json(res, 413, '上传文件过大'); return null; }
      chunks.push(chunk);
    }
  } catch {
    if (!res.destroyed) json(res, 400, '请求正文读取失败');
    return null;
  }
  return Buffer.concat(chunks);
}

function apiRoute(value) {
  if (typeof value !== 'string' || value.length > 8192 || value.includes('#')) return null;
  const [path] = value.split('?');
  return /^[A-Za-z0-9_-]+(?:\/[A-Za-z0-9_-]+)*$/.test(path) ? value : null;
}

export function createHandler({ backendPort = 7863 } = {}, { onApiKey } = {}) {
  if (!Number.isInteger(backendPort) || backendPort < 1 || backendPort > 65535) throw new Error('backendPort must be an integer from 1 to 65535');
  // Each machine keeps its Go service on loopback. DSH carries the registered
  // plugin route to the selected machine; this is never an arbitrary URL proxy.
  const backend = `http://127.0.0.1:${backendPort}`;
  // 自动连接引导与旧版登录接口无需预先持有凭证；后端验证请求来源或登录信息。
  const isLogin = (path, method) => (method === 'POST' && path === 'auth/login') || (method === 'GET' && path === 'auth/bootstrap');
  async function forward(req, res, { path, method, authorization, contentType, body }) {
    if (!isLogin(path, method) && (!/^Bearer \S+$/i.test(authorization) || authorization.length > 8192)) return json(res, 401, '请输入当前电脑账号服务的管理密钥');
    if (!apiRoute(path)) return json(res, 400, 'Invalid API path');
    const gatewayKey = authorization.replace(/^Bearer /i, '');
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 330_000);
    const disconnected = () => { if (!res.writableEnded) controller.abort(); };
    res.once('close', disconnected);
    try {
      const upstream = await fetch(`${backend}/panel/api/${path}`, {
        method, redirect: 'manual', signal: controller.signal,
        headers: { authorization, 'content-type': contentType || 'application/json' },
        body: method === 'POST' ? body : undefined,
      });
      // 后端认可的密钥顺带同步进 DSH 凭证库（pi-ai 网关路由按次解析它）；
      // 401 说明密钥无效，不同步避免写坏凭证。
      if (onApiKey && !isLogin(path, method) && upstream.ok && gatewayKey) onApiKey(gatewayKey).catch(() => {});
      if (upstream.status >= 300 && upstream.status < 400) return json(res, 502, '账号服务返回了不支持的重定向');
      const response = Buffer.from(await upstream.arrayBuffer());
      if (res.destroyed) return;
      res.writeHead(upstream.status, {
        'content-type': upstream.headers.get('content-type') || 'application/json', 'cache-control': 'no-store',
      });
      res.end(response);
    } catch {
      if (!res.destroyed) json(res, 502, `无法连接当前电脑的账号服务（127.0.0.1:${backendPort}），请确认该电脑的后端已启动`);
    } finally {
      clearTimeout(timeout);
      res.off('close', disconnected);
    }
  }
  return async (req, res) => {
    res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Referrer-Policy', 'no-referrer');
    if (!localRequest(req)) return json(res, 403, 'AI 工具箱仅支持从运行 DSH 的本机访问');
    const url = new URL(req.url, 'http://local.invalid');
    if (!url.pathname.startsWith(PREFIX)) return json(res, 404, 'Not found');
    const relative = url.pathname.slice(PREFIX.length);
    if (relative === 'runtime' && req.method === 'GET') {
      res.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'no-store' });
      return res.end(JSON.stringify({ protocol: 1 }));
    }
    if (relative === 'bridge') {
      if (req.method !== 'POST') return json(res, 405, 'Method not allowed');
      if (!/^application\/json(?:\s*;|$)/i.test(req.headers['content-type'] || '')) return json(res, 415, 'Expected JSON');
      const raw = await readBody(req, res, 44 * 1024 * 1024);
      if (raw === null) return;
      let input;
      try { input = JSON.parse(raw); } catch { return json(res, 400, 'Invalid bridge request'); }
      if (!input || !['GET', 'POST'].includes(input.method) || !apiRoute(input.path)
        || typeof input.authorization !== 'string'
        || (input.contentType != null && (typeof input.contentType !== 'string' || input.contentType.length > 512 || /[\r\n]/.test(input.contentType)))
        || (input.body !== undefined && (input.method !== 'POST' || typeof input.body !== 'string'
          || input.body.length > 44 * 1024 * 1024 || input.body.length % 4 || /[^A-Za-z0-9+/=]/.test(input.body)))) {
        return json(res, 400, 'Invalid bridge request');
      }
      const body = input.body === undefined ? undefined : Buffer.from(input.body, 'base64');
      if (body?.length > 32 * 1024 * 1024) return json(res, 413, '上传文件不能超过 32 MB');
      if (body && body.toString('base64') !== input.body) return json(res, 400, 'Invalid bridge body');
      return forward(req, res, { ...input, body });
    }
    if (!relative.startsWith('api/')) {
      const file = relative || 'index.html';
      if (!assets.has(file) || !['GET', 'HEAD'].includes(req.method)) return json(res, 404, 'Not found');
      try {
        const body = await readFile(new URL('../public/' + file, import.meta.url));
        res.writeHead(200, {
          'content-type': assets.get(file), 'cache-control': 'no-cache',
          'content-security-policy': "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self' data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'",
          'x-frame-options': 'SAMEORIGIN',
        });
        return res.end(req.method === 'HEAD' ? undefined : body);
      } catch { return json(res, 503, '页面尚未构建，请运行 npm run build'); }
    }
    if (!['GET', 'POST'].includes(req.method)) return json(res, 405, 'Method not allowed');
    // Fixed prefix + plain path prevents encoded traversal outside /panel/api/.
    const path = relative.slice(4) + url.search;
    if (!apiRoute(path)) return json(res, 400, 'Invalid API path');
    const authorization = req.headers.authorization || '';
    if (!isLogin(path, req.method) && !/^Bearer \S+$/i.test(authorization)) return json(res, 401, '请输入本机服务的管理密钥');
    const body = await readBody(req, res, 32 * 1024 * 1024);
    if (body === null) return;
    return forward(req, res, { path, method: req.method, authorization, contentType: req.headers['content-type'], body });
  };
}
