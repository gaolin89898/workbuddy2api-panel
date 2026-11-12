window.__ModuleLoader__.load({
  id: 'dsh-workbuddy-accounts',
  factory: require => {
    const { createElement: h, useEffect, useRef, useState, useSyncExternalStore } = require('react');
    const panel = 'workbuddy-accounts';
    const pages = [
      { id: 'accounts', label: '账号', path: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75', circle: [9, 7, 4] },
      { id: 'tasks', label: '任务中心', path: 'M9 11l3 3L22 4M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11' },
      { id: 'rewards', label: '活动奖励', path: 'M3 8h18v4H3zM5 12v9h14v-9M12 8v13M12 8H8.5A3.5 3.5 0 1 1 12 4.5V8zm0 0h3.5A3.5 3.5 0 1 0 12 4.5V8z' },
      { id: 'settings', label: '签到设置', path: 'M8 2v4M16 2v4M3 10h18M5 4h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2' },
      { id: 'logs', label: '运行日志', path: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M8 13h8M8 17h8' },
    ];
    function AccountIcon({ size = 18 } = {}) {
      return h('svg', { width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.7, 'aria-hidden': true },
        h('circle', { cx: 12, cy: 8, r: 4 }),
        h('path', { d: 'M4 21v-2a8 8 0 0 1 16 0v2' }));
    }
    // 页面内自带第二列菜单。侧栏行用极简组件（0.1.38 的 label-only 注册不渲染行）；
    // 曾疑似引发 whale bubbleTtlSweep 循环，实为 dsh-better-sidebar 0.30.1 的布局
    // 风暴（升级 0.32.1 后消失），与行组件无关。
    function AccountRow({ size = 16 }) {
      return h('span', { style: { display: 'inline-flex' } }, h(AccountIcon, { size }));
    }
    return {
      name: 'dsh-workbuddy-accounts', inject: ['slots', 'layout'],
      apply(ctx) {
        let current = 'accounts';
        const listeners = new Set();
        const getSnapshot = () => current;
        const subscribe = listener => { listeners.add(listener); return () => listeners.delete(listener); };
        const setPage = id => {
          if (current === id || !pages.some(page => page.id === id)) return;
          current = id;
          for (const listener of listeners) listener();
        };
        function AccountConsole() {
          const selected = useSyncExternalStore(subscribe, getSnapshot);
          const frame = useRef(null), ready = useRef(false), detach = useRef(null);
          const source = useRef(`/workbuddy-accounts/?dsh=1#${selected}`);
          const remote = !!window.__DSH_REMOTE_SESSION__?.peerId && window.__DSH_REMOTE_SESSION__.peerId !== 'local';
          const [frameDocument, setFrameDocument] = useState(null), [error, setError] = useState(''), [attempt, setAttempt] = useState(0);
          useEffect(() => {
            if (!remote) return;
            const abort = new AbortController(), blobs = [];
            let active = true;
            const read = async file => {
              const response = await window.fetch(new URL('/workbuddy-accounts/' + file, document.baseURI).href, { signal: abort.signal });
              if (!response.ok) throw new Error(response.status === 404 ? '请将远程电脑的 AI 工具箱更新到 0.1.9 或更新版本' : `读取远程 AI 工具箱失败：HTTP ${response.status}`);
              return response;
            };
            setError(''); setFrameDocument(null); ready.current = false;
            void (async () => {
              const runtime = await (await read('runtime')).json();
              if (runtime.protocol !== 1) throw new Error('远程 AI 工具箱版本不兼容，请更新插件');
              // Browser iframe navigations bypass DSH's fetch transport. Fetch
              // both resources through the host, then mount that exact build.
              const [script, style] = await Promise.all(['accounts.js', 'index.css'].map(async file => (await read(file)).text()));
              if (!active) return;
              const asset = (text, type) => {
                const url = URL.createObjectURL(new Blob([text], { type })); blobs.push(url); return url;
              };
              const js = asset(script, 'text/javascript'), css = asset(style, 'text/css');
              setFrameDocument(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src blob:; style-src blob: 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self' data:; base-uri 'none'; form-action 'none'"><title>AI 工具箱</title><link rel="stylesheet" href="${css}"></head><body><div id="app"></div><script type="module" src="${js}"></script></body></html>`);
            })().catch(cause => { if (active && cause.name !== 'AbortError') setError(cause.message); });
            return () => { active = false; ready.current = false; abort.abort(); for (const url of blobs) URL.revokeObjectURL(url); };
          }, [remote, attempt]);
          useEffect(() => {
            if (ready.current && frame.current) frame.current.contentWindow.location.hash = selected;
          }, [selected]);
          useEffect(() => () => detach.current?.(), []);
          const loaded = () => {
            detach.current?.();
            const target = frame.current.contentWindow;
            ready.current = true;
            target.location.hash = getSnapshot();
            const changed = () => setPage(target.location.hash.slice(1));
            target.addEventListener('hashchange', changed);
            detach.current = () => target.removeEventListener('hashchange', changed);
          };
          if (remote && !frameDocument) return h('div', { role: error ? 'alert' : 'status', style: { padding: 24, color: 'var(--dsw-alias-label-primary)' } },
            error || '正在连接远程 AI 工具箱…', error && h('button', { onClick: () => setAttempt(value => value + 1), style: { marginLeft: 12 } }, '重新连接'));
          return h('iframe', {
            ref: frame, onLoad: loaded, ...(remote ? { srcDoc: frameDocument } : { src: source.current }), title: 'AI 工具箱', 'data-ai-toolbox': '1',
            style: { display: 'block', width: '100%', height: '100%', flex: 1, border: 0, minWidth: 0, minHeight: 0 },
          });
        }
        ctx.slots.inject('sidebar.panellist', () => {
          const offPanel = ctx.slots.register({ name: 'main', key: panel }, AccountConsole);
          const offRow = ctx.slots.register({
            name: 'sidebar.panellist', id: panel, order: 60, label: () => 'AI 工具箱',
          }, AccountRow);
          return () => {
            if (ctx.layout.panelInfo.getSnapshot().activePanelId === panel) ctx.layout.selectPanel(null);
            if (typeof offRow === 'function') offRow();
            if (typeof offPanel === 'function') offPanel();
          };
        });
      },
    };
  },
});
