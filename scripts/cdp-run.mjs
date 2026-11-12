// One-shot: attach to the already-open DSH page, click 签到设置 in the plugin
// iframe, wait for the freeze, then sample JS stacks via Debugger.pause.
import http from 'node:http';
import { createRequire } from 'node:module';
const WebSocket = createRequire(import.meta.url)('C:/Users/32891/Desktop/dsh/workbuddy2api-panel/dsh-plugin/node_modules/ws/index.js');

const DEBUG_PORT = process.env.DEBUG_PORT || 9333;
const get = path => new Promise((resolve, reject) => {
  http.get({ host: '127.0.0.1', port: DEBUG_PORT, path, headers: { host: '127.0.0.1' } }, res => {
    let d = ''; res.on('data', c => d += c); res.on('end', () => resolve(d));
  }).on('error', reject);
});
const sleep = ms => new Promise(r => setTimeout(r, ms));

function connect(wsUrl, handlers) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    let id = 0; const pending = {};
    ws.onopen = async () => resolve({
      send: (method, params = {}) => new Promise(res => {
        const i = ++id; pending[i] = res; ws.send(JSON.stringify({ id: i, method, params }));
      }),
      close: () => ws.close(),
    });
    ws.onmessage = m => {
      const d = JSON.parse(m.data);
      if (d.id && pending[d.id]) { pending[d.id](d); delete pending[d.id]; return; }
      handlers?.(d);
    };
    ws.on('error', e => reject(new Error(`ws error ${e.code || ''} ${e.message}`)));
  });
}

await sleep(6000); // let DSH UI + plugin client load
let page;
if (process.env.PAGE_WS) {
  page = { webSocketDebuggerUrl: process.env.PAGE_WS };
} else {
  const list = JSON.parse(await get('/json/list'));
  page = list.find(t => t.type === 'page' && t.url.includes(':3080'));
}
if (!page) { console.log('DSH page target not found'); process.exit(1); }
console.log('attaching:', (page.url || page.webSocketDebuggerUrl).slice(0, 60));

let samples = 0;
const scripts = {};
const pageConn = await connect(page.webSocketDebuggerUrl, d => {
  if (d.method === 'Debugger.scriptParsed') scripts[d.params.scriptId] = d.params.url;
  if (d.method === 'Debugger.paused') {
    samples++;
    console.log(`--- sample ${samples} ---`);
    for (const f of d.params.callFrames.slice(0, 10)) {
      const url = f.url || scripts[f.location?.scriptId] || `script:${f.location?.scriptId}`;
      console.log(`  ${f.functionName || '(anonymous)'} @${url.slice(0, 120)}:${f.location?.lineNumber}:${f.location?.columnNumber}`);
    }
    pageConn.send('Debugger.resume');
    if (samples >= 6) setTimeout(() => process.exit(0), 200);
  }
});
await pageConn.send('Debugger.enable');

const state = await pageConn.send('Runtime.evaluate', { expression: `
  (() => {
    const row = [...document.querySelectorAll('button, [role="button"], a, span, div')]
      .filter(e => e.textContent.trim() === 'AI 工具箱' && e.offsetParent !== null).pop();
    if (!row) return 'NO SIDEBAR ROW';
    (row.closest('button, [role="button"]') || row).click();
    return 'CLICKED sidebar row';
  })()`, returnByValue: true });
console.log('step1:', state.result?.result?.value);
let iframeOk = 'NO IFRAME after wait';
for (let i = 0; i < 10; i++) {
  await sleep(1000);
  const check = await pageConn.send('Runtime.evaluate', { expression: "!!document.querySelector('iframe[data-ai-toolbox]')", returnByValue: true });
  if (check.result?.result?.value) { iframeOk = 'IFRAME READY'; break; }
}
console.log('step2:', iframeOk);
const click = await pageConn.send('Runtime.evaluate', { expression: `
  (() => {
    const f = document.querySelector('iframe[data-ai-toolbox]');
    if (!f || !f.contentDocument) return 'NO IFRAME DOC';
    const btns = [...f.contentDocument.querySelectorAll('.side-navigation button')];
    const btn = btns.find(b => b.textContent.includes(process.env.CLICK_TARGET || '签到设置'));
    if (!btn) return 'NO BUTTON, have: ' + btns.map(b => b.textContent.trim()).join('|');
    btn.click();
    return 'CLICKED 签到设置';
  })()`, returnByValue: true });
console.log('step3:', click.result?.result?.value);
console.log('--- sampling immediately ---');
for (let round = 0; round < 6; round++) {
  await pageConn.send('Debugger.pause');
  await sleep(400);
}
setTimeout(() => { console.log(`(done, ${samples} samples captured)`); process.exit(0); }, 4000);
