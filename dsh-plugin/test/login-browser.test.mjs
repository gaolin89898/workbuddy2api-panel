import test from 'node:test';
import assert from 'node:assert/strict';
import { createHostRuntime } from '../../web/src/hostRuntime.js';

function setup(popup) {
  const calls = [];
  const host = {
    document: { baseURI: 'http://localhost/' }, localStorage: {}, fetch,
    __DSH_REMOTE_SESSION__: { peerId: 'home' },
    open: (...args) => { calls.push(args); return popup; },
  };
  const child = { parent: host, frameElement: { getAttribute: () => '1' } };
  return { host, calls, runtime: createHostRuntime(child) };
}

test('browser login reserves a host tab before the asynchronous request and detaches its opener', async () => {
  const navigations = [];
  let closes = 0;
  const popup = { opener: {}, closed: false, location: { replace: url => navigations.push(url) }, close: () => closes++ };
  const { runtime, calls } = setup(popup);
  const login = runtime.prepareBrowserLogin();
  assert.deepEqual(calls, [['about:blank', '_blank']]);
  assert.equal(popup.opener, null);
  await Promise.resolve();
  login.open('https://www.codebuddy.cn/login?state=test');
  assert.deepEqual(navigations, ['https://www.codebuddy.cn/login?state=test']);
  login.close();
  assert.equal(closes, 0, 'closing the dialog leaves the authorization page open');
});

test('desktop shells receive the final HTTPS URL when blank popups are denied', () => {
  const { runtime, calls } = setup(null);
  runtime.prepareBrowserLogin().open('https://www.workbuddy.ai/login');
  assert.deepEqual(calls.at(-1), ['https://www.workbuddy.ai/login', '_blank', 'noopener,noreferrer']);
});

test('cancelled browser logins close pending tabs without affecting a later attempt', () => {
  let closes = 0;
  const { runtime } = setup({ close: () => closes++ });
  const first = runtime.prepareBrowserLogin(), second = runtime.prepareBrowserLogin();
  first.close();
  first.close();
  assert.equal(closes, 1);
  second.close();
  assert.equal(closes, 2);
});

test('invalid URLs and machine changes cannot navigate the login tab', () => {
  const { host, runtime, calls } = setup(null);
  const login = runtime.prepareBrowserLogin();
  assert.throws(() => login.open('javascript:alert(1)'), /授权地址无效/);
  host.__DSH_REMOTE_SESSION__.peerId = 'local';
  assert.throws(() => login.open('https://www.codebuddy.cn/'), /已切换电脑/);
  assert.equal(calls.length, 1);
});
