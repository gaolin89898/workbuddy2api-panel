import test from 'node:test';
import assert from 'node:assert/strict';
import { chinaDate, clockTime, formatDateTime } from '../../web/src/dateTime.js';

test('date formatting preserves Beijing day and month boundaries', () => {
  assert.equal(chinaDate('2026-10-31T15:59:59Z'), '2026-10-31');
  assert.equal(chinaDate('2026-10-31T16:00:00Z'), '2026-11-01');
  assert.equal(chinaDate('2024-02-29T15:59:59Z'), '2024-02-29');
  assert.equal(clockTime('2026-11-12T16:00:00Z'), '00:00:00');
  for (const value of [null, '', '0001-01-01T00:00:00Z']) assert.equal(clockTime(value), '--');
  assert.equal(clockTime('invalid'), 'Invalid Date');
  for (const value of ['2026-11-12T16:00:00Z', '2024-02-29T15:59:59Z', 1794499200000]) {
    assert.equal(formatDateTime(value), new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }));
  }
});

test('formatting a full log buffer reuses Intl formatters', async () => {
  const original = Intl.DateTimeFormat;
  let constructed = 0;
  Intl.DateTimeFormat = new Proxy(original, {
    construct(target, args) { constructed++; return Reflect.construct(target, args); },
  });
  try {
    const helpers = await import('../../web/src/dateTime.js?formatter-reuse');
    for (let index = 0; index < 3500; index++) {
      const value = Date.UTC(2026, 10, 13) - index * 60000;
      helpers.chinaDate(value);
      helpers.clockTime(value);
      helpers.formatDateTime(value);
    }
    assert.ok(constructed <= 3, `created ${constructed} formatters for log rows`);
  } finally {
    Intl.DateTimeFormat = original;
  }
});
