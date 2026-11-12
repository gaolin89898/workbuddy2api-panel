// CDP repro driver: open DSH web UI, click "签到设置" inside the plugin iframe.
const browser = process.argv[2];
const dshUrl = process.argv[3];
const ws = new WebSocket(browser);
let id = 0; const pending = {};
function send(method, params = {}, sessionId) {
  return new Promise(res => { const i = ++id; pending[i] = res; ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
}
ws.onmessage = m => { const d = JSON.parse(m.data); if (d.id && pending[d.id]) { pending[d.id](d); delete pending[d.id]; } };
ws.onerror = e => { console.log('WS ERROR'); process.exit(1); };
ws.onopen = async () => {
  const created = await send('Target.createTarget', { url: dshUrl });
  const targetId = created.result.targetId;
  console.log('tab created:', targetId);
  await new Promise(r => setTimeout(r, 8000));
  const attached = await send('Target.attachToTarget', { targetId, flatten: true });
  const sessionId = attached.result.sessionId;
  const check = await send('Runtime.evaluate', { expression: "(() => { const f = document.querySelector('iframe[data-ai-toolbox]'); return f ? 'iframe ok' : 'NO IFRAME'; })()", returnByValue: true }, sessionId);
  console.log('check:', check.result?.result?.value);
  const click = await send('Runtime.evaluate', { expression: `
    (() => {
      const f = document.querySelector('iframe[data-ai-toolbox]');
      if (!f || !f.contentDocument) return 'no iframe doc';
      const btns = [...f.contentDocument.querySelectorAll('.side-navigation button')];
      const btn = btns.find(b => b.textContent.includes('签到设置'));
      if (!btn) return 'no button, have: ' + btns.map(b => b.textContent.trim()).join('|');
      btn.click();
      return 'clicked 签到设置';
    })()`, returnByValue: true }, sessionId);
  console.log('click:', click.result?.result?.value);
  console.log('TARGET=' + targetId + ' SESSION=' + sessionId);
  ws.close();
  process.exit(0);
};
