// An iframe has its own fetch and Storage objects. Use the host's transport
// and machine-scoped storage when DSH embeds the toolbox.
export function createHostRuntime(win) {
  const embedded = win.parent !== win && (win.frameElement?.getAttribute('data-ai-toolbox') === '1'
    || new URLSearchParams(win.location.search).get('dsh') === '1');
  const host = embedded ? win.parent : win;
  const peerId = host.__DSH_REMOTE_SESSION__?.peerId || 'local';
  const remote = embedded && peerId !== 'local';
  const fetchHost = host.fetch.bind(host);
  const storage = host.localStorage;
  const bridge = new URL('/workbuddy-accounts/bridge', host.document.baseURI);
  const apiBase = embedded ? new URL('/workbuddy-accounts/api/', host.document.baseURI) : null;

  function assertTarget() {
    if ((host.__DSH_REMOTE_SESSION__?.peerId || 'local') !== peerId) {
      throw new Error('已切换电脑，请重新打开 AI 工具箱');
    }
  }

  function prepareBrowserLogin() {
    assertTarget();
    // Reserve a tab during the click gesture. Desktop shells deny blank tabs
    // and handle the final HTTPS URL with the system browser instead.
    let popup = host.open('about:blank', '_blank');
    if (popup) popup.opener = null;
    return {
      open(value) {
        assertTarget();
        const url = new URL(value);
        if (url.protocol !== 'https:') throw new Error('授权地址无效');
        if (popup && !popup.closed) {
          popup.location.replace(url.href);
          popup = null;
        } else host.open(url.href, '_blank', 'noopener,noreferrer');
      },
      close() { popup?.close(); popup = null; },
    };
  }

  async function fetchAPI(input, options = {}) {
    assertTarget();
    if (!remote) return fetchHost(String(input), options);
    const request = new Request(String(input), options);
    const url = new URL(request.url);
    if (url.protocol !== apiBase.protocol || url.host !== apiBase.host || !url.pathname.startsWith(apiBase.pathname)) throw new Error('无效的账号接口地址');
    let body;
    if (!['GET', 'HEAD'].includes(request.method)) {
      const bytes = new Uint8Array(await request.arrayBuffer());
      // The remote-session carrier base64 encodes this envelope once more.
      if (bytes.length > 16 * 1024 * 1024) throw new Error('远程导入文件不能超过 16 MB');
      let binary = '';
      for (let offset = 0; offset < bytes.length; offset += 32768) binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
      body = win.btoa(binary);
    }
    assertTarget();
    // Remote-session deliberately strips Authorization headers. Carry the Go
    // API key inside this same-origin POST, never in URLs or DSH cookies.
    const response = await fetchHost(bridge.href, {
      method: 'POST', signal: options.signal, headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path: url.pathname.slice(apiBase.pathname.length) + url.search,
        method: request.method, authorization: request.headers.get('authorization') || '',
        contentType: request.headers.get('content-type'), body }),
    });
    assertTarget();
    return response;
  }

  return { embedded, remote, peerId, storage, apiBase, fetchAPI, prepareBrowserLogin };
}

export const hostRuntime = typeof window === 'undefined' ? null : createHostRuntime(window);
