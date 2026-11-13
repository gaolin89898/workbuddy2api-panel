const timeZone = 'Asia/Shanghai';
// Reuse ICU formatters across table rows and repeated page mounts.
const dateFormatter = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' });
const timeFormatter = new Intl.DateTimeFormat('zh-CN', { timeZone, hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', { timeZone, hour12: false, year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' });

export function chinaDate(value = Date.now()) {
  return dateFormatter.format(new Date(value));
}

export function clockTime(value) {
  if (!value || String(value).startsWith('0001-')) return '--';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Invalid Date' : timeFormatter.format(date);
}

export function formatDateTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Invalid Date' : dateTimeFormatter.format(date);
}
