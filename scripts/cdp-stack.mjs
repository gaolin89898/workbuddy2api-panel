// Capture JS call stacks from a busy renderer via CDP Debugger.pause sampling.
const pageWs = process.argv[2];
const ws = new WebSocket(pageWs);
let id = 0; const pending = {}; let sessionId = null;
let samples = 0; const maxSamples = 3;
function send(method, params = {}) {
  return new Promise(res => { const i = ++id; pending[i] = res; ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
}
ws.onmessage = m => {
  const d = JSON.parse(m.data);
  if (d.id && pending[d.id]) { pending[d.id](d); delete pending[d.id]; return; }
  if (d.method === 'Debugger.paused') {
    samples++;
    console.log(`--- sample ${samples} ---`);
    for (const f of d.params.callFrames.slice(0, 12)) {
      const loc = f.location ? ` @${(f.url || f.location.scriptId || '?').split('/').pop()}:${f.location.lineNumber}:${f.location.columnNumber}` : '';
      console.log(`  ${f.functionName || '(anonymous)'}${loc}`);
    }
    send('Debugger.resume');
    if (samples >= maxSamples) { setTimeout(() => process.exit(0), 500); }
  }
};
ws.onopen = async () => {
  const attached = await send('Target.attachToTarget', { flatten: true });
  sessionId = attached.result.sessionId;
  await send('Debugger.enable');
  await send('Debugger.pause');
};
ws.onerror = () => { console.log('WS ERROR'); process.exit(1); };
setTimeout(() => { console.log(`(timeout after ${samples} samples)`); process.exit(samples > 0 ? 0 : 1); }, 15000);
