import { createRequire } from 'node:module';
import assert from 'node:assert/strict';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser = await chromium.launch({ headless: true, channel: process.env.BROWSER_CHANNEL || undefined });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const watchdog = setTimeout(() => { console.log('Diagnostic timeout'); void browser.close(); }, 30000);
  watchdog.unref();
  page.setDefaultTimeout(8000);
  const errors = [];
  page.on('pageerror', error => { errors.push(error.message); console.log('PAGE ERROR:', error.message); });
  page.on('console', message => { if (message.type() === 'warning' || message.type() === 'error') console.log('CONSOLE:', message.text().slice(0, 2400)); });
  if (process.env.PANEL_DEV_URL) {
    await page.route(url => /^\/(panel\/)?(src\/|node_modules\/|@vite\/|@id\/)/.test(url.pathname), async route => {
      const url = new URL(route.request().url());
      const response = await route.fetch({ url: process.env.PANEL_DEV_URL + url.pathname + url.search });
      await route.fulfill({ response });
    });
    await page.route(url => url.pathname === '/workbuddy-accounts/', route => route.fulfill({
      contentType: 'text/html', body: '<html><body><div id="app"></div><script type="module" src="/panel/src/main.js"></script></body></html>',
    }));
  }
  page.on('response', response => {
    if (/\/rewards|\/packages/.test(new URL(response.url()).pathname)) console.log('DATA RESPONSE:', new URL(response.url()).pathname.replace(/\/benefits\/accounts\/[^/]+/, '/benefits/accounts/[uid]'), response.status());
  });
  await page.addInitScript(key => { if (key) localStorage.setItem('wb2api.key', key); }, process.env.PANEL_KEY);
  const cdp = await page.context().newCDPSession(page);
  const scripts = new Map();
  cdp.on('Debugger.scriptParsed', event => scripts.set(event.scriptId, event.url));
  cdp.on('Debugger.paused', event => {
    console.log('PAUSED STACK:', event.callFrames.slice(0, 12).map(frame => ({ name: frame.functionName,
      url: scripts.get(frame.location.scriptId), line: frame.location.lineNumber + 1, column: frame.location.columnNumber + 1 })));
    void cdp.send('Debugger.resume');
  });
  await cdp.send('Debugger.enable');
  await page.goto(process.env.DSH_URL || 'http://127.0.0.1:3081', { waitUntil: 'domcontentloaded' });
  console.log('INITIAL UI:', (await page.locator('body').innerText()).slice(0, 1200));
  await page.getByText('AI 工具箱', { exact: true }).last().click();
  const frame = page.frameLocator('iframe[data-ai-toolbox]');
  await frame.locator('.connection').filter({ hasText: '服务已连接' }).waitFor();
  for (const label of ['活动奖励', '账号', '活动奖励', '签到设置']) {
    console.log('NAVIGATING:', label);
    const timer = setTimeout(() => { console.log('Navigation is unresponsive; sampling renderer'); void cdp.send('Debugger.pause'); }, 10000);
    try {
      await frame.locator('.side-navigation button').getByText(label, { exact: true }).click();
      await frame.locator('.side-navigation button.active').getByText(label, { exact: true }).waitFor();
      if (label === '活动奖励') {
        await page.waitForTimeout(6000);
        console.log('REWARD CARDS:', await frame.locator('.reward-item').count());
      }
      await frame.locator('main').evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)), { timeout: 8000 });
      console.log('NAVIGATION OK:', label);
    } finally { clearTimeout(timer); }
  }
  assert.deepEqual(errors, []);
} finally { await browser.close(); }
