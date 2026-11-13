import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { chinaDate } from '../web/src/dateTime.js';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.PANEL_URL || 'http://127.0.0.1:5173';
const panelPath = process.env.PANEL_PATH || '/';
const browser = await chromium.launch({ headless: true, channel: process.env.BROWSER_CHANNEL || undefined });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const now = Date.now();
  const logs = Array.from({ length: 500 }, (_, index) => ({
    ts: new Date(now - index * 60000).toISOString(), ch: 'task', text: 'checkin 签到成功',
  }));
  const accounts = Array.from({ length: 20 }, (_, index) => ({
    uid: 'test-' + index, nickname: 'Test ' + index, credits: 100, credits_total: 200,
    realm: 'cn', checkin_done: true, benefits_supported: true,
  }));
  await page.route('**/workbuddy-accounts/api/**', route => {
    const path = new URL(route.request().url()).pathname.split('/api/')[1];
    const responses = {
      overview: { accounts }, logs: { entries: logs },
      'settings/checkin': { settings: { checkin_enabled: true, checkin_hours: [9, 21], refresh_after_checkin: true, catchup_on_start: true } },
      'desktop/status': { clients: [] }, capabilities: { gateway: true },
      'tasks/queue': { running: false, items: [] },
      packages: { accounts: accounts.map(account => ({ ...account, remain: 100, size: 200, packages: [
        { name: 'Later', remain: 20, expires_at: '2027-10-01T00:00:00+08:00' },
        { name: 'Earliest', remain: 30, expires_at: '2027-02-01T00:00:00+08:00' },
        { name: 'Expired balance', remain: 0, expires_at: '2026-01-01T00:00:00+08:00' },
        { name: 'Middle', remain: 50, expires_at: '2027-03-01T00:00:00+08:00' },
      ] })) },
      credit_history: { entries: logs.map((entry, index) => ({ time: entry.ts, uid: accounts[index % accounts.length].uid, delta: 1, after: 100 })) },
    };
    const rewards = { compensation: { active: false }, chances: 0, vouchers: [], streak: {
      streak: { days: 2 }, makeup_cards: { balance: 0 },
      redemption_status: { tier_7_status: 'locked', tier_14_status: 'locked', tier_28_status: 'locked', tiers: [7, 14, 28].map(days => ({ tier: String(days), days, credit: days * 10 })) },
    } };
    return route.fulfill({ json: path.endsWith('/rewards') ? rewards : responses[path] || {} });
  });
  await page.route('**/__embed-performance__', route => route.fulfill({
    contentType: 'text/html',
    body: `<html><body style="margin:0;--dsw-alias-bg-base:#fff"><iframe data-ai-toolbox="1" src="${panelPath}?dsh=1" style="width:100%;height:100vh;border:0"></iframe></body></html>`,
  }));
  await page.goto(base + '/__embed-performance__');
  const frame = page.frameLocator('iframe');
  await frame.locator('.connection').filter({ hasText: '服务已连接' }).waitFor();
  if (!await frame.locator('.platform-select').count()) throw new Error('Platform selector missing on embedded accounts page: ' + (await frame.locator('body').innerText()).slice(0, 400));
  const platform = frame.locator('.platform-select');
  assert.ok((await platform.textContent()).includes('WorkBuddy'));
  assert.ok((await platform.textContent()).includes('2'));
  await platform.click();
  await frame.locator('.arco-select-option').filter({ hasText: 'Trae' }).click();
  assert.ok((await platform.textContent()).includes('Trae'));
  await platform.click();
  await frame.locator('.arco-select-option').filter({ hasText: 'WorkBuddy' }).click();
  const results = [];
  for (let round = 0; round < 2; round++) {
    for (const [label, selector] of [['签到设置', '.schedule-view'], ['活动奖励', '.benefits-rewards'], ['任务中心', '.benefits-tasks'], ['活动奖励', '.benefits-rewards'], ['运行日志', '.logs-view'], ['积分明细', '.billing-view'], ['账号', '.account-view']]) {
      await frame.locator('.side-navigation button').getByText(label, { exact: true }).evaluate(button => {
        window.navigationStarted = performance.now();
        button.click();
      });
      await frame.locator(selector).waitFor();
      if (label === '签到设置') {
        const counts = await frame.locator('.history-day small').allTextContents();
        assert.deepEqual(counts, Array.from({ length: 7 }, (_, index) => {
          if (index === 6) return '20 / 20';
          const date = chinaDate(now - (6 - index) * 86400000);
          const count = logs.filter(entry => chinaDate(entry.ts) === date).length;
          return count ? `${count} 次` : '--';
        }));
      }
      if (label === '运行日志') assert.equal(await frame.locator('.log-row').count(), 500);
      if (label === '活动奖励') {
        await frame.locator('.benefit-packages tbody tr').first().waitFor();
        assert.equal(await frame.locator('.reward-item').count(), 4);
        const expected = new Date('2027-02-01T00:00:00+08:00').toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
        assert.equal(await frame.locator('.benefit-packages tbody tr').first().locator('td').nth(3).textContent(), expected);
        await frame.locator('.benefit-packages tbody tr').first().getByText('明细', { exact: true }).click();
        assert.deepEqual(await frame.locator('.package-detail-row strong').allTextContents(), ['Later', 'Earliest', 'Expired balance', 'Middle']);
        await frame.locator('.benefit-packages tbody tr').first().getByText('收起', { exact: true }).click();
      }
      if (label === '积分明细') {
        await frame.locator('.ledger-list').getByText('+1', { exact: true }).first().waitFor();
        assert.equal(await frame.locator('.ledger-list tbody tr').count(), 500);
        assert.equal(await frame.locator('.acct-line').count(), 20);
        const month = chinaDate(now).slice(0, 7);
        const income = logs.filter((entry, index) => index % accounts.length === 0 && chinaDate(entry.ts).slice(0, 7) === month).length;
        assert.ok((await frame.locator('.acct-line .benefit-detail').first().textContent()).includes(`本月 ${income ? '+' + income : '--'} / --`));
      }
      const elapsed = await frame.locator(selector).evaluate(async () => {
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        return Math.round(performance.now() - window.navigationStarted);
      });
      results.push({ round: round + 1, page: label, milliseconds: elapsed });
    }
  }
  assert.deepEqual(errors, []);
  console.table(results);
  console.log('Embedded navigation passed with 500 logs and 20 accounts.');
} finally {
  await browser.close();
}
