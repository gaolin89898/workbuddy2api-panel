// Pull script source from the live DSH renderer via Debugger.getScriptSource.
import { createRequire } from 'node:module';
const WebSocket = createRequire(import.meta.url)('C:/Users/32891/Desktop/dsh/workbuddy2api-panel/dsh-plugin/node_modules/ws/index.js');
import http from 'node:http';
const DEBUG_PORT = process.env.DEBUG_PORT || 9444;
const MATCH = process.env.SCRIPT_MATCH || 'dsh-whale/widget.js';
const get = path => new Promise((resolve, reject) => {
  http.get({ host: '127.0.0.1', port: DEBUG_PORT, path, headers: { host: '127.0.0.1' } }, res => {
    let d = ''; res.on('data', c => d += c); res.on('end', () => resolve(d));
  }).on('error', reject);
});
const sleep = ms => new Promise(r => setTimeout(r, ms));
let page;
if (process.env.PAGE_WS) {
  page = { webSocketDebuggerUrl: process.env.PAGE_WS };
} else {
  const list = JSON.parse(await get('/json/list'));
  page = list.find(t => t.type === 'page' && t.url.includes('dsh-app'));
}
if (!page) { console.log('no dsh-app page target'); process.exit(1); }
const ws = new WebSocket(page.webSocketDebuggerUrl);
let id = 0; const pending = {}; let sessionId = null;
const scripts = {};
ws.on('message', m => {
  let d; try { d = JSON.parse(m.toString()); } catch { return; }
  if (d.id && pending[d.id]) { pending[d.id](d); delete pending[d.id]; return; }
  if (d.method === 'Debugger.scriptParsed' && (d.params.url || '').includes(MATCH)) {
    scripts[d.params.scriptId] = d.params.url;
  }
});
ws.on('open', async () => {
  await new Promise(res => { const i = ++id; pending[i] = res; ws.send(JSON.stringify({ id: i, method: 'Debugger.enable', params: {} })); });
  await sleep(3000);
  const found = Object.entries(scripts);
  console.log('matched scripts:', found.length);
  for (const [scriptId, url] of found) {
    const src = await new Promise(res => { const i = ++id; pending[i] = res; ws.send(JSON.stringify({ id: i, method: 'Debugger.getScriptSource', params: { scriptId } })); });
    const text = src.result?.scriptSource || '';
    console.log(`=== ${url.slice(0, 100)}... (${text.length} chars) ===`);
    const lines = text.split('\n');
    const ranges = process.env.SHOW_LINES ? process.env.SHOW_LINES.split(',').map(x => x.split(':').map(Number)) : [[14292, 14301], [22312, 22321]];
    for (const [from, to] of ranges) {
      if (lines.length > from) {
        console.log(`--- lines ${from}..${to} (total ${lines.length}) ---`);
        console.log(lines.slice(from - 1, to).map((l, i) => (from + i) + ': ' + l.slice(0, 220)).join('\n'));
      } else console.log(`(script has ${lines.length} lines, no line ${from})`);
    }
  }
  process.exit(0);
});
ws.on('error', e => { console.log('ws err', e.message); process.exit(1); });
setTimeout(() => { console.log('TIMEOUT'); process.exit(1); }, 20000);
