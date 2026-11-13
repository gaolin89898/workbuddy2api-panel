<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Alert as AAlert, Button as AButton, Empty as AEmpty, Input as AInput, InputPassword as AInputPassword, Message, Select as ASelect, Option as AOption, Scrollbar as AScrollbar, Switch as ASwitch, Textarea as ATextarea, Tooltip as ATooltip } from '@arco-design/web-vue';
import { IconCopy, IconLink, IconRefresh, IconSearch, IconArrowRight, IconStorage, IconSchedule, IconSwap, IconSettings, IconCodeSquare, IconFile, IconSave, IconThunderbolt, IconSafe, IconArrowRise } from '@arco-design/web-vue/es/icon';
import { clockTime, formatNumber } from '../useAccounts';
import workbuddyIcon from '../assets/platform-workbuddy.png';
import traeIcon from '../assets/platform-trae.png';
import qoderIcon from '../assets/platform-qoder.png';
import minimaxIcon from '../assets/platform-minimax.png';
import loomyIcon from '../assets/platform-loomy.png';
import antigravityIcon from '../assets/platform-antigravity.svg';
import doubaoIcon from '../assets/platform-doubao.png';

// GatewayView 网关（设计稿 GATEWAY · 用量/模型/请求日志/网关设置 四页合一）：
// 页头 Segments 切换 + Gateway Status 卡 + 带坐标轴的用量图表 + Live Requests 流；
// 模型页带目录汇总与排序；请求日志带汇总条与导出；设置页分组切换与配置概览。
const props = defineProps({ model: Object });
const section = ref('usage');
const sections = [
  { id: 'usage', name: '用量' },
  { id: 'models', name: '模型' },
  { id: 'requests', name: '请求日志' },
  { id: 'settings', name: '网关设置' },
];
const loading = ref(false);
const supplier = ref('auto'), supplierReady = ref(false), supplierSaving = ref(false);
const suppliers = [
  { id: 'auto', name: '自动' },
  { id: 'workbuddy-cn', name: 'WorkBuddy 国内版', icon: workbuddyIcon },
  { id: 'workbuddy-global', name: 'WorkBuddy 国际版', icon: workbuddyIcon },
  { id: 'trae', name: 'Trae', icon: traeIcon },
  { id: 'qoder', name: 'Qoder', icon: qoderIcon },
  { id: 'minimax', name: 'MiniMax', icon: minimaxIcon },
  { id: 'loomy', name: 'Loomy', icon: loomyIcon },
  { id: 'antigravity', name: 'Antigravity', icon: antigravityIcon },
  { id: 'doubao', name: '豆包', icon: doubaoIcon },
];
const supplierById = Object.fromEntries(suppliers.map(item => [item.id, item]));
let modelLoadVersion = 0;
const usage = ref(null), models = ref(null), probes = ref({}), settings = ref(null), restartRequired = ref([]);
const overview = computed(() => props.model.overview || {});
const availableAccounts = computed(() => {
  if (supplier.value.startsWith('workbuddy-')) {
    const realm = supplier.value === 'workbuddy-global' ? 'global' : 'cn';
    return (overview.value.accounts || []).filter(account => (account.realm || 'cn') === realm && !account.disabled && !account.paused && !account.cooling).length;
  }
  if (supplier.value !== 'auto') return Number(overview.value.provider_totals?.[supplier.value]?.healthy || 0);
  return Number(overview.value.healthy ?? overview.value.total ?? 0) + Object.values(overview.value.provider_totals || {}).reduce((sum, provider) => sum + Number(provider.healthy || 0), 0);
});
const usageWindow = ref(24);
const usageDim = ref('account');
const modelSearch = ref(''), modelRealm = ref(''), modelSort = ref('default');
const reqOutcome = ref(''), reqLimit = ref(100), reqRange = ref(0), reqQuery = ref('');
const requests = ref(null), archiveState = ref(null);
const requestMetrics = ref(null);
const usagePeriod = computed(() => usageWindow.value === 24 ? '最近 24 小时' : `最近 ${usageWindow.value / 24} 天`);
// 24 小时按小时聚合，3 天及以上按天聚合。
const usageDaily = computed(() => usageWindow.value >= 72);
const endpoint = computed(() => location.origin + '/v1/chat/completions');
function copyText(value, label = '已复制') {
  navigator.clipboard.writeText(value).then(() => Message.success(label)).catch(() => Message.error('复制失败'));
}

async function load(target = section.value) {
  if (!props.model.capabilities?.gateway) return;
  loading.value = true;
  try {
    if (target === 'usage') {
      const [u, feed, metrics] = await Promise.all([
        props.model.api('usage?hours=' + usageWindow.value),
        props.model.api('request_logs?limit=8').catch(() => ({ entries: [] })),
        props.model.api('request_metrics').catch(() => null),
      ]);
      usage.value = u;
      feedRows.value = feed.entries || [];
      archiveState.value = metrics?.archive || null;
      requestMetrics.value = metrics;
    } else if (target === 'models') {
      if (!supplierReady.value && !await ensureConfig()) return;
      const version = ++modelLoadVersion;
      const [md, pr] = await Promise.all([props.model.api('models'), props.model.api('model_probes').catch(() => ({}))]);
      if (version !== modelLoadVersion) return;
      models.value = md;
      probes.value = pr.probes || {};
    } else if (target === 'requests') {
      const params = new URLSearchParams({ limit: String(reqLimit.value) });
      if (reqOutcome.value) params.set('outcome', reqOutcome.value);
      if (reqRange.value) params.set('from', String(Math.floor(Date.now() / 1000) - Number(reqRange.value)));
      const [logs, metrics] = await Promise.all([
        props.model.api('request_logs?' + params.toString()),
        props.model.api('request_metrics').catch(() => null),
      ]);
      requests.value = logs;
      archiveState.value = metrics?.archive || null;
    } else if (target === 'settings') {
      const data = await props.model.api('settings/gateway');
      applyGatewayConfig(data.config || {});
      settings.value = data;
      restartRequired.value = [];
      hydrate(data.config || {});
    }
  } catch (cause) { Message.error(cause.message); }
  finally { loading.value = false; }
}
watch(section, value => {
  if (value === 'usage' && !usage.value) load(value);
  else if (value === 'models' && !models.value) load(value);
  else if (value === 'requests' && !requests.value) load(value);
  else if (value === 'settings' && !form.value) load(value);
});
let feedTimer;
onMounted(() => {
  load(section.value);
  feedTimer = setInterval(async () => {
    if (section.value !== 'usage' || document.hidden) return;
    const [feed, metrics] = await Promise.all([
      props.model.api('request_logs?limit=8').catch(() => null),
      props.model.api('request_metrics').catch(() => null),
    ]);
    if (feed) feedRows.value = feed.entries || [];
    if (metrics) requestMetrics.value = metrics;
  }, 5000);
});
onBeforeUnmount(() => clearInterval(feedTimer));

// ── 网关状态卡（设计稿：单行 Status Top + 五段 Metrics）──────────
const statusMetrics = computed(() => {
  const totals = usage.value?.totals || {};
  const metrics = requestMetrics.value;
  const requests = Number(metrics?.completed ?? totals.requests ?? 0);
  const errors = Number(metrics?.failed ?? totals.errors ?? 0);
  const latency = metrics?.avg_duration_ms ?? avgLatency.value;
  const locks = (overview.value.model_locks || []).length;
  const inFlight = metrics?.in_flight ?? (overview.value.accounts || []).reduce((sum, account) => sum + Number(account.in_flight || 0), 0);
  return [
    { label: '请求', value: formatNumber(requests), sub: metrics ? '本次运行' : usagePeriod.value },
    { label: '成功率', value: requests ? ((requests - errors) / requests * 100).toFixed(1) + '%' : '--', sub: `异常 ${formatNumber(errors)}` },
    { label: '平均延迟', value: latency ? (latency / 1000).toFixed(1) + 's' : '--', sub: avgTTFB.value ? '首字 ' + (avgTTFB.value / 1000).toFixed(1) + 's' : '最近请求' },
    { label: '在途', value: String(inFlight), sub: `/ ${configCache.value?.pool?.max_in_flight ?? '--'} · 粘性 ${overview.value.sticky_sessions ?? 0}` },
    { label: '模型锁', value: String(locks), sub: locks ? (overview.value.model_locks[0]?.model || '限流中') : '无限流', warning: locks > 0 },
  ];
});
const avgLatency = computed(() => {
  const rows = feedRows.value.filter(row => row.duration_ms);
  return rows.length ? rows.reduce((sum, row) => sum + row.duration_ms, 0) / rows.length : 0;
});
const avgTTFB = computed(() => {
  const rows = feedRows.value.filter(row => row.ttfb_ms);
  return rows.length ? rows.reduce((sum, row) => sum + row.ttfb_ms, 0) / rows.length : 0;
});
const endpointChips = [
  { method: 'POST', path: '/v1/chat/completions' },
  { method: 'GET', path: '/v1/models' },
  { method: 'GET', path: '/healthz' },
];

// ── 用量 ────────────────────────────────────────────────────────
const totals = computed(() => usage.value?.totals || {});
const dimRows = computed(() => usage.value ? (usageDim.value === 'account' ? usage.value.by_account : usageDim.value === 'model' ? usage.value.by_model : usage.value.by_realm) || [] : []);
// 时序图按「所选窗口」铺满时间轴：服务端只返回有数据的分片，数据只有几小时时
// 切 24h/3 天/7 天/30 天会看到完全一样的柱子，被当成"点了没反应"。这里把窗口内的
// 分片补齐（无数据填 0），切换窗口即刻看到坐标轴范围变化。
const CHINA_OFFSET = 8 * 3600000;
const HOUR_MS = 3600000, DAY_MS = 86400000;
function chinaBucketStart(ms, daily) {
  const step = daily ? DAY_MS : HOUR_MS;
  return Math.floor((ms + CHINA_OFFSET) / step) * step - CHINA_OFFSET;
}
function chinaBucketKey(ms, daily) {
  const date = new Date(ms + CHINA_OFFSET);
  const pad = value => String(value).padStart(2, '0');
  const day = `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}`;
  return daily ? day : `${day}T${pad(date.getUTCHours())}`;
}
function pointBucketStart(point) {
  const value = String(point.t || '');
  const timestamp = point.scope === 'day' ? Date.parse(value + 'T00:00:00+08:00') : Date.parse(value + ':00:00+08:00');
  return isNaN(timestamp) ? null : timestamp;
}
const series = computed(() => {
  const daily = usageDaily.value;
  const step = daily ? DAY_MS : HOUR_MS;
  const count = Math.max(1, Math.round(usageWindow.value / (daily ? 24 : 1)));
  const end = chinaBucketStart(Date.now(), daily);
  const start = end - (count - 1) * step;
  const buckets = Array.from({ length: count }, (_, index) => ({
    scope: daily ? 'day' : 'hour',
    t: chinaBucketKey(start + index * step, daily),
    prompt_tokens: 0,
    completion_tokens: 0,
    requests: 0,
  }));
  for (const point of usage.value?.series || []) {
    const timestamp = pointBucketStart(point);
    if (timestamp == null) continue;
    // 按天聚合时，小时分片要落到它所属的那一天，而不是要求整点对齐。
    const index = Math.floor((chinaBucketStart(timestamp, daily) - start) / step);
    if (index < 0 || index >= count) continue;
    buckets[index].prompt_tokens += Number(point.prompt_tokens || 0);
    buckets[index].completion_tokens += Number(point.completion_tokens || 0);
    buckets[index].requests += Number(point.requests || 0);
  }
  return buckets;
});
const seriesMax = computed(() => Math.max(1, ...series.value.map(p => Number((p.prompt_tokens || 0) + (p.completion_tokens || 0)))));
const chartTicks = computed(() => [1, 2 / 3, 1 / 3, 0].map(ratio => ({ ratio, label: seriesMax.value < 3 ? String(Number((seriesMax.value * ratio).toFixed(1))) : fmtK(Math.round(seriesMax.value * ratio)) })));
const peakPoint = computed(() => series.value.reduce((best, point) => Number((point.prompt_tokens || 0) + (point.completion_tokens || 0)) > Number((best?.prompt_tokens || 0) + (best?.completion_tokens || 0)) ? point : best, null));
// 悬停提示：原生 title 要停顿一秒才弹、还会被图表裁掉，这里自己画一个。
const hoverIndex = ref(-1);
const hoverBar = computed(() => {
  const point = series.value[hoverIndex.value];
  if (!point) return null;
  const count = series.value.length || 1;
  const prompt = Number(point.prompt_tokens || 0), completion = Number(point.completion_tokens || 0);
  const total = prompt + completion;
  const height = Math.min(100, total / seriesMax.value * 100);
  const flip = height > 55;
  return {
    left: Math.min(92, Math.max(8, (hoverIndex.value + 0.5) / count * 100)) + '%',
    top: Math.max(0, 100 - height) + '%',
    transform: flip ? 'translate(-50%, 8px)' : 'translate(-50%, calc(-100% - 8px))',
    time: pointTime(point),
    prompt: formatNumber(prompt),
    completion: formatNumber(completion),
    total: formatNumber(total),
    requests: Number(point.requests || 0),
  };
});
const feedRows = ref([]);
function fmtK(value) { const n = Number(value || 0); return n >= 1000 ? Math.round(n / 1000) + 'K' : String(n); }
function compactTokens(value) {
  const n = Number(value || 0);
  if (n >= 1000000) return (n / 1000000).toFixed(2) + 'M';
  if (n >= 1000) return Number((n / 1000).toFixed(1)) + 'K';
  return String(n);
}
function fmtTokens(value) { return formatNumber(value); }
function fmtCost(value) { return Number(value || 0).toFixed(2); }
function barHeights(point) {
  return {
    prompt: Number(point.prompt_tokens || 0) / seriesMax.value * 100 + '%',
    completion: Number(point.completion_tokens || 0) / seriesMax.value * 100 + '%',
  };
}
function pointTime(point) {
  const timestamp = point.scope === 'hour' && point.t.length === 13 ? point.t + ':00:00+08:00' : point.scope === 'day' && point.t.length === 10 ? point.t + 'T00:00:00+08:00' : point.t;
  const date = new Date(timestamp);
  if (isNaN(date)) return '';
  const options = usageDaily.value ? { month: '2-digit', day: '2-digit' } : { hour: '2-digit', minute: '2-digit', hour12: false };
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', ...options }).format(date);
}
function xLabel(point, index, count) {
  if (index % Math.ceil(count / 6) === 0) return pointTime(point);
  return '';
}
function pointTitle(point) { return `${point.t}\n输入 ${formatNumber(point.prompt_tokens)} · 输出 ${formatNumber(point.completion_tokens)}`; }
function switchWindow(value) { usageWindow.value = value; load('usage'); }
function outcomeName(row) { return ({ ok: '成功', http_error: 'HTTP 错误', stream_error: '流错误', interrupted: '中断', client_error: '客户端错误' })[row.outcome] || row.outcome || '--'; }

// ── KPI 三段 + 模型用量表 + 运行参数（设计稿 Insights Row）──────
const chartKpis = computed(() => {
  const totals = usage.value?.totals || {};
  const prompt = Number(totals.prompt_tokens || 0), completion = Number(totals.completion_tokens || 0);
  const perK = totals.total_tokens > 0 ? (totals.credits / totals.total_tokens * 1000).toFixed(2) : '--';
  return [
    { label: 'Tokens', value: compactTokens(totals.total_tokens), sub: `输入 ${compactTokens(prompt)} · 输出 ${compactTokens(completion)}` },
    { label: '积分消耗', value: fmtCost(totals.credits), sub: totals.total_tokens > 0 ? perK + ' 积分 / 1K tokens' : '--', accent: true },
    { label: '缓存命中', value: totals.cache_hit_tokens || totals.cache_miss_tokens ? Number(totals.cache_hit_rate || 0).toFixed(1) + '%' : '--', sub: `命中 ${fmtK(totals.cache_hit_tokens)} · 未命中 ${fmtK(totals.cache_miss_tokens)}` },
  ];
});
const modelUsageRows = computed(() => {
  const rows = (usage.value?.by_model || []).slice(0, 8);
  const max = Math.max(1, ...rows.map(row => Number(row.total_tokens || 0)));
  return rows.map(row => ({ ...row, barWidth: Number(row.total_tokens || 0) / max * 100, realm: row.realm === 'global' || row.key.startsWith('global:') ? '国际' : row.realm === 'cn' || row.key.startsWith('cn:') ? '国内' : '' }));
});
const configCache = ref(null);
let configLoading;
function applyGatewayConfig(config) {
  const nextSupplier = config.gateway?.supplier || 'auto';
  if (nextSupplier !== supplier.value) {
    ++modelLoadVersion;
    models.value = null;
    modelRealm.value = '';
  }
  configCache.value = config;
  supplier.value = nextSupplier;
  supplierReady.value = true;
}
async function ensureConfig() {
  if (configCache.value) return configCache.value;
  if (!configLoading) {
    configLoading = props.model.api('settings/gateway').then(data => {
      applyGatewayConfig(data.config || {});
      return configCache.value;
    }).catch(cause => { Message.error(cause.message); return null; }).finally(() => { configLoading = null; });
  }
  return configLoading;
}
async function changeSupplier(value) {
  if (value === supplier.value || supplierSaving.value) return;
  supplierSaving.value = true;
  try {
    await props.model.api('settings/gateway', { method: 'POST', body: JSON.stringify({ gateway: { supplier: value } }) });
    ++modelLoadVersion;
    applyGatewayConfig({ ...configCache.value, gateway: { ...configCache.value?.gateway, supplier: value } });
    if (settings.value) settings.value.config = { ...settings.value.config, gateway: configCache.value.gateway };
    models.value = null;
    modelRealm.value = '';
    Message.success('供应应用已切换为' + supplierById[value].name);
    if (section.value === 'models') await load('models');
  } catch (cause) { Message.error(cause.message); }
  finally { supplierSaving.value = false; }
}
const runtimeRows = computed(() => {
  const c = configCache.value || {};
  const strategyName = value => ({ weighted_random: '加权随机', round_robin: '轮询', sequential_exhaust: '顺序耗尽', single_account: '单账号耗尽' })[value] || value || '--';
  return [
    { row: '调度策略', value: c.pool?.strategy?.domestic === c.pool?.strategy?.international ? strategyName(c.pool?.strategy?.domestic) : strategyName(c.pool?.strategy?.domestic) + ' / ' + strategyName(c.pool?.strategy?.international) },
    { row: '在途上限', value: `单号 ${c.pool?.max_in_flight ?? '--'} · 全局 ${c.pool?.max_in_flight_global ?? '--'}` },
    { row: '熔断冷却', value: `${c.pool?.breaker_threshold ?? '--'} 次 · ${c.pool?.breaker_cooldown ?? '--'}` },
    { row: '会话粘性', value: c.session_sticky?.enabled ? `${c.session_sticky.ttl} · GC ${c.session_sticky.gc_interval}` : '关闭' },
    { row: '请求归档', value: c.logging?.request_archive_enabled ? `开启 · 保留 ${c.logging.request_retention_days ?? '--'} 天` : '关闭' },
  ];
});
watch(section, value => { if (value === 'usage') ensureConfig(); });
onMounted(() => ensureConfig());

// ── 模型 ────────────────────────────────────────────────────────
const modelRows = computed(() => {
  const rows = (models.value?.models || []).filter(row =>
    (!modelRealm.value || row.id.startsWith(modelRealm.value + ':') || row.id.startsWith(modelRealm.value + '/')) &&
    (!modelSearch.value || (row.id + ' ' + (row.name || '') + ' ' + (row.description || '') + ' ' + (row.vendor || '')).toLowerCase().includes(modelSearch.value.toLowerCase())));
  const sorted = [...rows];
  if (modelSort.value === 'credits') sorted.sort((a, b) => (a.credits ?? 9e9) - (b.credits ?? 9e9));
  else if (modelSort.value === 'context') sorted.sort((a, b) => (b.context_length || 0) - (a.context_length || 0));
  else if (modelSort.value === 'output') sorted.sort((a, b) => (b.max_output_tokens || 0) - (a.max_output_tokens || 0));
  else if (modelSort.value === 'name') sorted.sort((a, b) => a.id.localeCompare(b.id));
  return sorted;
});
const modelMetrics = computed(() => {
  const rows = models.value?.models || [];
  return [
    { label: '模型', value: String(rows.length), sub: supplierById[models.value?.supplier || supplier.value]?.name || '自动' },
    { label: '支持思考', value: String(rows.filter(row => row.supports_reasoning).length), sub: 'reasoning' },
    { label: '支持工具', value: String(rows.filter(row => row.supports_tool_call).length), sub: 'tool call' },
    { label: '视觉', value: String(rows.filter(row => row.supports_images).length), sub: 'images' },
  ];
});
function probeOf(id) { const bare = id.includes(':') ? id.slice(id.indexOf(':') + 1) : id; return probes.value[id] || probes.value[bare] || probes.value[Object.keys(probes.value).find(key => key.endsWith(':' + bare))] || null; }
function probeCell(row) {
  const probe = probeOf(row.id);
  if (!probe) return { text: row.max_output_tokens ? fmtK(row.max_output_tokens) : '--', kind: '', tip: '' };
  const tip = `声称 ${probe.claimed ? fmtK(probe.claimed) : '?'} · 实测 ${probe.measured ? fmtK(probe.measured) : '?'}${probe.note ? ' · ' + probe.note : ''}${probe.tested_at ? ' · 探测于 ' + probe.tested_at : ''}`;
  if (probe.verdict === 'clamped' && probe.measured) return { text: fmtK(probe.measured) + ' ⚠', kind: 'warning-text', tip };
  if (probe.verdict === 'at_least' && probe.measured) return { text: '≥' + fmtK(probe.measured), kind: '', tip };
  return { text: '?', kind: '', tip: tip || '未测出' };
}
function rateCell(row) {
  if (row.promo_factor != null && row.promo_credits) return { html: true, value: `<b>${row.promo_credits}</b>${row.promo_label ? ' <small>' + row.promo_label + '</small>' : ''}${row.credits ? ' <s>' + row.credits + '</s>' : ''}` };
  return { html: false, value: row.credits ?? '--' };
}
const capabilityTags = row => [
  row.supports_tool_call && '工具',
  row.supports_images && '视觉',
  row.supports_reasoning && '思考',
  row.is_default && '默认',
].filter(Boolean);

// ── 请求日志 ────────────────────────────────────────────────────
const requestRows = computed(() => (requests.value?.entries || []).filter(row =>
  !reqQuery.value || [row.model, row.account, row.client_ip, row.user_agent, row.request_id, row.path].some(field => (field || '').toLowerCase().includes(reqQuery.value.toLowerCase()))));
const requestSummary = computed(() => {
  const rows = requestRows.value;
  const errors = rows.filter(row => row.outcome !== 'ok').length;
  const ttfb = rows.filter(row => row.ttfb_ms);
  return [
    { label: '请求', value: formatNumber(rows.length), sub: '本次读取' },
    { label: '成功', value: formatNumber(rows.length - errors), sub: 'outcome ok' },
    { label: '异常', value: formatNumber(errors), sub: rows.length ? (errors / rows.length * 100).toFixed(1) + '%' : '--' },
    { label: '平均 TTFB', value: ttfb.length ? Math.round(ttfb.reduce((sum, row) => sum + row.ttfb_ms, 0) / ttfb.length) + 'ms' : '--', sub: `${ttfb.length} 条记录` },
    { label: '归档', value: archiveState.value?.enabled ? '开启' : '关闭', sub: archiveState.value?.enabled ? `保留 ${archiveState.value.retention_days ?? '?'} 天` : '仅内存指标' },
  ];
});
function exportRequests() {
  const header = '时间,结果,状态,模型,账号,来源IP,耗时ms,TTFBms,Tokens,积分,请求ID';
  const lines = requestRows.value.map(row => [
    new Date(row.time).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }),
    row.outcome || '', row.status || '', (row.model || row.path || '').replace(/,/g, ' '),
    (row.account || '').replace(/,/g, ' '), row.client_ip || '', row.duration_ms || 0,
    row.ttfb_ms || 0, row.total_tokens || 0, row.credit_known ? row.credit : '', row.request_id || '',
  ].join(','));
  const blob = new Blob(['\ufeff' + [header, ...lines].join('\n')], { type: 'text/csv;charset=utf-8' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `请求日志_${new Date().toISOString().slice(0, 10)}.csv`;
  link.click();
  URL.revokeObjectURL(link.href);
}

// ── 网关设置（左侧分组导航 + 配置摘要 + 右侧分组表单）───────────
const form = ref(null);
const saving = ref(false);
const activeSection = ref('access');
const settingsGroups = [
  { id: 'access', name: '服务与安全', icon: IconStorage },
  { id: 'schedule', name: '定时任务', icon: IconSchedule },
  { id: 'pool', name: '选号与治理', icon: IconSwap },
  { id: 'upstream', name: '上游与会话', icon: IconSettings },
  { id: 'prompt', name: '提示词', icon: IconCodeSquare },
  { id: 'models', name: '模型与日志', icon: IconFile },
];
const activeSettingsGroup = computed(() => settingsGroups.find(group => group.id === activeSection.value));
const savedAt = ref('');
const configOverview = computed(() => {
  const c = settings.value?.config || {};
  return [
    { row: '监听地址', value: c.listen || '--' },
    { row: '管理鉴权', value: c.api_key ? 'Bearer · 已启用' : '未启用', accent: !!c.api_key },
    { row: '在途上限', value: `${c.pool?.max_in_flight ?? '--'} · 全局 ${c.pool?.max_in_flight_global ?? '--'}` },
    { row: '会话粘性', value: c.session_sticky?.enabled ? `${c.session_sticky.ttl} · GC ${c.session_sticky.gc_interval}` : '关闭' },
    { row: '熔断', value: `${c.pool?.breaker_threshold ?? '--'} 次 · ${c.pool?.breaker_cooldown ?? '--'}` },
    { row: '归档保留', value: c.logging?.request_archive_enabled ? `${c.logging.request_retention_days ?? '--'} 天 · ${c.logging.request_archive_max_mb ?? '--'} MiB` : '关闭' },
    { row: 'Redis', value: props.model.capabilities?.redis_mode === 'upstash' ? 'Upstash 镜像' : '内存模式' },
    { row: '成本探索', value: c.pool?.cost_explore_interval || '--' },
    { row: '指纹脱敏', value: c.features?.sanitize_blacklist_fingerprints ? '开启' : '关闭' },
    { row: '快过期优先', value: c.pool?.prefer_expiring ? `开启 · ${c.pool.expiring_soon || '--'}` : '关闭' },
  ];
});
function hydrate(config) {
  const hours = list => (list || []).join(', ');
  form.value = {
    listen: config.listen || '', api_key: config.api_key || '',
    schedule: {
      keepalive_enabled: !!config.schedule?.keepalive_enabled, keepalive_hours: hours(config.schedule?.keepalive_hours),
      travel_enabled: !!config.schedule?.travel_enabled, travel_hours: hours(config.schedule?.travel_hours),
      activity_enabled: !!config.schedule?.activity_enabled, activity_hours: hours(config.schedule?.activity_hours),
      growth_enabled: !!config.schedule?.growth_enabled, growth_hours: hours(config.schedule?.growth_hours),
      streak_enabled: !!config.schedule?.streak_enabled,
      task_sweep_enabled: !!config.schedule?.task_sweep_enabled, task_sweep_minutes: config.schedule?.task_sweep_minutes ?? 15,
      balance_refresh_enabled: !!config.schedule?.balance_refresh_enabled, balance_refresh_minutes: config.schedule?.balance_refresh_minutes ?? 5,
      include_disabled_in_tasks: !!config.schedule?.include_disabled_in_tasks,
    },
    pool: {
      strategy_domestic: config.pool?.strategy?.domestic || 'single_account', strategy_international: config.pool?.strategy?.international || 'single_account',
      order_domestic: (config.pool?.account_order?.domestic || []).join(', '), order_international: (config.pool?.account_order?.international || []).join(', '),
      max_in_flight: config.pool?.max_in_flight ?? 3, max_in_flight_global: config.pool?.max_in_flight_global ?? 2,
      breaker_threshold: config.pool?.breaker_threshold ?? 3, breaker_cooldown: config.pool?.breaker_cooldown || '30m', breaker_cooldown_max: config.pool?.breaker_cooldown_max || '6h',
      degrade_threshold: config.pool?.degrade_threshold ?? 5, degrade_cooldown: config.pool?.degrade_cooldown || '10m', degrade_cooldown_max: config.pool?.degrade_cooldown_max || '2h',
      idle_weight_per_hour: config.pool?.idle_weight_per_hour ?? 0.5, idle_weight_max: config.pool?.idle_weight_max ?? 5,
      cost_explore_interval: config.pool?.cost_explore_interval || '30m', credit_floor: config.pool?.credit_floor ?? 0,
      expiring_soon: config.pool?.expiring_soon || '168h', prefer_expiring: !!config.pool?.prefer_expiring,
      cross_domain_fallback: !!config.pool?.cross_domain_fallback,
    },
    cooldown: { soft_rate: config.cooldown?.soft_rate || '600s', soft_rate_max: config.cooldown?.soft_rate_max || '2h' },
    session_sticky: { enabled: !!config.session_sticky?.enabled, ttl: config.session_sticky?.ttl || '30m', gc_interval: config.session_sticky?.gc_interval || '5m' },
    upstream: { timeout_seconds: config.upstream?.timeout_seconds ?? 120, header_timeout_seconds: config.upstream?.header_timeout_seconds ?? 120, idle_timeout_seconds: config.upstream?.idle_timeout_seconds ?? 300, user_agent: config.upstream?.user_agent || '' },
    prompt: { mode: config.prompt?.mode || 'passthrough', file: config.prompt?.file || '' },
    features: { sanitize_blacklist_fingerprints: !!config.features?.sanitize_blacklist_fingerprints },
    models: { hidden: (config.models?.hidden || []).join('\n') },
    logging: { request_client_info: !!config.logging?.request_client_info, request_archive_enabled: !!config.logging?.request_archive_enabled, request_retention_days: config.logging?.request_retention_days ?? 7, request_archive_max_mb: config.logging?.request_archive_max_mb ?? 100 },
    server: { read_timeout: config.server?.read_timeout || '300s' },
  };
}
function resetSettings() { if (settings.value) { hydrate(settings.value.config || {}); restartRequired.value = []; Message.info('已放弃未保存的修改'); } }
function jumpToGroup(id) { activeSection.value = id; }
const splitList = value => value.split(/[,，\s]+/).map(item => item.trim()).filter(Boolean);
async function saveSettings() {
  saving.value = true;
  try {
    const f = form.value;
    const payload = {
      listen: f.listen, api_key: f.api_key,
      schedule: {
        keepalive_enabled: f.schedule.keepalive_enabled, keepalive_hours: splitList(f.schedule.keepalive_hours),
        travel_enabled: f.schedule.travel_enabled, travel_hours: splitList(f.schedule.travel_hours),
        activity_enabled: f.schedule.activity_enabled, activity_hours: splitList(f.schedule.activity_hours),
        growth_enabled: f.schedule.growth_enabled, growth_hours: splitList(f.schedule.growth_hours),
        streak_enabled: f.schedule.streak_enabled,
        task_sweep_enabled: f.schedule.task_sweep_enabled, task_sweep_minutes: Number(f.schedule.task_sweep_minutes) || 15,
        balance_refresh_enabled: f.schedule.balance_refresh_enabled, balance_refresh_minutes: Number(f.schedule.balance_refresh_minutes) || 5,
        include_disabled_in_tasks: f.schedule.include_disabled_in_tasks,
      },
      pool: {
        strategy: { domestic: f.pool.strategy_domestic, international: f.pool.strategy_international },
        account_order: { domestic: splitList(f.pool.order_domestic), international: splitList(f.pool.order_international) },
        max_in_flight: Number(f.pool.max_in_flight) || 0, max_in_flight_global: Number(f.pool.max_in_flight_global) || 2,
        breaker_threshold: Number(f.pool.breaker_threshold) || 3, breaker_cooldown: f.pool.breaker_cooldown, breaker_cooldown_max: f.pool.breaker_cooldown_max,
        degrade_threshold: Number(f.pool.degrade_threshold) || 5, degrade_cooldown: f.pool.degrade_cooldown, degrade_cooldown_max: f.pool.degrade_cooldown_max,
        idle_weight_per_hour: Number(f.pool.idle_weight_per_hour) || 0.5, idle_weight_max: Number(f.pool.idle_weight_max) || 5,
        cost_explore_interval: f.pool.cost_explore_interval, credit_floor: Number(f.pool.credit_floor) || 0,
        expiring_soon: f.pool.expiring_soon, prefer_expiring: f.pool.prefer_expiring, cross_domain_fallback: f.pool.cross_domain_fallback,
      },
      cooldown: f.cooldown,
      session_sticky: f.session_sticky,
      upstream: { timeout_seconds: Number(f.upstream.timeout_seconds) || 120, header_timeout_seconds: Number(f.upstream.header_timeout_seconds) || 120, idle_timeout_seconds: Number(f.upstream.idle_timeout_seconds) || 300, user_agent: f.upstream.user_agent },
      prompt: f.prompt,
      features: f.features,
      models: { hidden: f.models.hidden.split('\n').map(line => line.trim()).filter(Boolean) },
      logging: { ...f.logging, request_retention_days: Number(f.logging.request_retention_days) || 7, request_archive_max_mb: Number(f.logging.request_archive_max_mb) || 100 },
      server: f.server,
    };
    const data = await props.model.api('settings/gateway', { method: 'POST', body: JSON.stringify(payload) });
    settings.value = { ...settings.value, config: { ...settings.value?.config, ...payload } };
    configCache.value = settings.value.config;
    savedAt.value = clockTime(Date.now());
    restartRequired.value = data.restart_required || [];
    Message.success('网关设置已保存' + (restartRequired.value.length ? `；需重启生效：${restartRequired.value.join('、')}` : '（已热生效）'));
  } catch (cause) { Message.error(cause.message); }
  finally { saving.value = false; }
}
</script>

<template>
  <section class="gateway-view" :class="{ 'gateway-settings-active': section === 'settings', 'gateway-usage-active': section === 'usage', 'gateway-models-active': section === 'models', 'gateway-requests-active': section === 'requests' }">
    <header class="page-heading">
      <a-select :model-value="supplier" class="platform-select gateway-supplier-select" aria-label="供应应用" :loading="supplierSaving" :disabled="!supplierReady || supplierSaving || saving" @change="changeSupplier">
        <template #label="{ data }"><span class="platform-option"><img v-if="supplierById[data.value]?.icon" class="platform-mark" :src="supplierById[data.value].icon" alt="" /><icon-swap v-else /><span>{{ supplierById[data.value]?.name }}</span></span></template>
        <a-option v-for="item in suppliers" :key="item.id" :value="item.id" :label="item.name"><span class="platform-option"><img v-if="item.icon" class="platform-mark" :src="item.icon" alt="" /><icon-swap v-else /><span>{{ item.name }}</span></span></a-option>
      </a-select>
      <div class="log-heading-actions">
        <div class="filter-tabs gateway-segments" role="group" aria-label="网关分区">
          <button v-for="item in sections" :key="item.id" :class="{ active: section === item.id }" :aria-pressed="section === item.id" @click="section = item.id">{{ item.name }}</button>
        </div>
        <a-tooltip content="刷新"><a-button :loading="loading" aria-label="刷新" @click="load(section)"><template #icon><icon-refresh /></template></a-button></a-tooltip>
      </div>
    </header>

    <template v-if="section === 'usage'">
      <section class="panel-card gateway-status">
        <div class="status-top">
          <div class="status-left">
            <span class="status-halo"><i /></span>
            <div class="status-text"><strong>服务在线</strong><small>已运行 {{ Math.floor((overview.uptime_sec || 0) / 86400) ? Math.floor(overview.uptime_sec / 86400) + ' 天 ' : '' }}{{ Math.floor((overview.uptime_sec || 0) % 86400 / 3600) }} 小时 · {{ availableAccounts }} 个账号可用</small></div>
            <i class="status-vdivider" />
            <div class="endpoint-chips">
              <span v-for="chip in endpointChips" :key="chip.path" class="endpoint-chip"><b :class="{ get: chip.method === 'GET' }">{{ chip.method }}</b>{{ chip.path }}</span>
            </div>
          </div>
          <div class="status-right">
            <span class="key-state"><icon-safe />API Key {{ overview.auth_required ? '已启用' : '未启用' }}</span>
            <a-button type="primary" class="copy-primary" size="small" @click="copyText(endpoint, '端点已复制')"><template #icon><icon-copy /></template>复制地址</a-button>
          </div>
        </div>
        <i class="status-hdivider" />
        <div class="status-metrics">
          <div v-for="metric in statusMetrics" :key="metric.label" class="status-metric" :class="{ 'metric-warning': metric.warning }"><span>{{ metric.label }}</span><div class="metric-value"><strong>{{ metric.value }}</strong><small>{{ metric.sub }}</small></div></div>
        </div>
      </section>

      <div class="usage-row">
      <section class="panel-card usage-trend">
        <header class="ledger-head">
          <div class="ledger-title"><h2>用量趋势</h2><small>{{ usagePeriod }} · 每{{ usageDaily ? '天' : '小时' }}聚合</small></div>
          <div class="filter-tabs usage-windows" role="group" aria-label="统计窗口"><button v-for="value in [24, 72, 168, 720]" :key="value" :class="{ active: usageWindow === value }" :aria-pressed="usageWindow === value" @click="switchWindow(value)">{{ value === 24 ? '24h' : value / 24 + ' 天' }}</button></div>
        </header>
        <div class="chart-kpis">
          <div v-for="kpi in chartKpis" :key="kpi.label" class="chart-kpi"><span>{{ kpi.label }}</span><strong :class="kpi.accent ? 'accent-text' : ''">{{ kpi.value }}</strong><small>{{ kpi.sub }}</small></div>
        </div>
        <div class="usage-chart" role="img" aria-label="Token 时序图">
          <div class="chart-row">
            <div class="chart-yaxis"><span v-for="tick in chartTicks" :key="tick.ratio">{{ tick.label }}</span></div>
            <div class="chart-plot">
              <div class="chart-grid"><i v-for="tick in chartTicks" :key="tick.ratio" :style="{ bottom: tick.ratio * 100 + '%' }" /></div>
              <div class="chart-bars">
                <div v-for="(point, index) in series" :key="index" class="usage-bar" :class="{ hovered: hoverIndex === index }" @mouseenter="hoverIndex = index" @mouseleave="hoverIndex = -1" :aria-label="pointTitle(point)">
                  <i class="seg completion" :style="{ height: barHeights(point).completion }" />
                  <i class="seg prompt" :style="{ height: barHeights(point).prompt }" />
                </div>
              </div>
              <div v-if="hoverBar" class="chart-tip" :style="{ left: hoverBar.left, top: hoverBar.top, transform: hoverBar.transform }">
                <strong>{{ hoverBar.time }}</strong>
                <span><i class="usage-swatch prompt" />输入 {{ hoverBar.prompt }}</span>
                <span><i class="usage-swatch completion" />输出 {{ hoverBar.completion }}</span>
                <em>合计 {{ hoverBar.total }}<template v-if="hoverBar.requests"> · {{ hoverBar.requests }} 次</template></em>
              </div>
              <a-empty v-if="!series.some(point => point.prompt_tokens || point.completion_tokens)" class="chart-empty" :description="loading ? '正在加载用量' : '暂无用量数据'" />
            </div>
          </div>
          <div class="chart-xlabels"><span v-for="(point, index) in series" :key="index">{{ xLabel(point, index, series.length) }}</span></div>
          <footer class="chart-foot"><span class="chart-peak"><icon-arrow-rise />峰值 {{ peakPoint && (peakPoint.prompt_tokens || peakPoint.completion_tokens) ? pointTime(peakPoint) + ' · ' + fmtK(Number(peakPoint.prompt_tokens || 0) + Number(peakPoint.completion_tokens || 0)) : '--' }} tokens</span><span class="usage-chart-legend"><i class="usage-swatch prompt" />输入<i class="usage-swatch completion" />输出</span></footer>
        </div>
      </section>
        <aside class="live-card">
          <header class="live-head"><div><i class="live-dot" /><h2>实时请求</h2><span class="live-badge">LIVE</span></div><span>5s</span></header>
          <a-scrollbar class="live-feed" outer-class="live-feed-wrap">
            <div v-for="row in feedRows" :key="row.request_id" class="feed-row" :class="{ 'feed-error': row.outcome !== 'ok' }">
              <div class="feed-line1"><span class="feed-model"><i class="live-dot" /><span class="mono" :title="row.model || row.path">{{ row.model || row.path }}</span></span><small>{{ (Number(row.duration_ms || 0) / 1000).toFixed(1) }}s</small></div>
              <div class="feed-line2"><time>{{ new Date(row.time).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) }}</time><span :title="row.account">{{ row.account || '--' }}</span><small v-if="row.outcome === 'ok'">{{ compactTokens(row.total_tokens) }} tok</small><small v-else class="feed-outcome">{{ row.status || '' }} {{ outcomeName(row) }}</small></div>
            </div>
            <a-empty v-if="!feedRows.length" description="暂无请求" />
          </a-scrollbar>
          <footer class="live-foot"><span><i class="live-dot" />成功率 {{ statusMetrics[1].value }} · {{ statusMetrics[0].value }} 次</span><button @click="section = 'requests'">全部请求<icon-arrow-right /></button></footer>
        </aside>
      </div>

      <div class="billing-layout insights-row">
        <section class="panel-card model-usage-card">
          <header class="ledger-head">
            <div class="ledger-title inline"><h2>模型用量</h2><small>{{ usagePeriod }}</small></div>
            <a-button size="small" type="text" @click="section = 'models'">模型目录<icon-arrow-right /></a-button>
          </header>
          <a-scrollbar class="table-scroll" outer-class="table-wrap"><table>
            <thead><tr><th>模型</th><th>请求</th><th>Tokens</th><th>积分</th><th>每百万</th><th>缓存</th></tr></thead>
            <tbody>
              <tr v-for="row in modelUsageRows" :key="row.key">
                <td><strong class="mono">{{ row.key }}</strong><small v-if="row.extra || row.realm" class="benefit-detail">{{ [row.extra, row.realm].filter(Boolean).join(' · ') }}</small></td>
                <td>{{ formatNumber(row.requests) }}</td>
                <td><div class="token-bar"><span class="token-track"><i :style="{ width: row.barWidth + '%' }" /></span><span>{{ fmtK(row.total_tokens) }}</span></div></td>
                <td :class="{ 'warning-text': row.credit_samples > 0 && !row.credits }">{{ fmtCost(row.credits) }}</td>
                <td>{{ row.credits_per_1m_tokens ? Number(row.credits_per_1m_tokens).toFixed(1) : '--' }}</td>
                <td>{{ row.cache_hit_tokens || row.cache_miss_tokens ? Number(row.cache_hit_rate || 0).toFixed(1) + '%' : '--' }}</td>
              </tr>
            </tbody>
          </table></a-scrollbar><a-empty v-if="!modelUsageRows.length" description="暂无模型用量" />
        </section>
        <aside class="panel-card runtime-card">
          <header class="ledger-head">
            <h2>运行参数</h2><span class="key-state">热生效</span>
          </header>
          <div class="overview-rows">
            <div v-for="row in runtimeRows" :key="row.row" class="overview-row"><span>{{ row.row }}</span><b>{{ row.value }}</b></div>
          </div>
          <footer class="runtime-foot"><span>同步自 config.json</span><a-button size="mini" type="text" @click="section = 'settings'">网关设置<icon-arrow-right /></a-button></footer>
        </aside>
      </div>
    </template>

    <template v-else-if="section === 'models'">
      <section class="panel-card models-summary">
        <div class="status-metrics"><div v-for="metric in modelMetrics" :key="metric.label" class="status-metric"><span>{{ metric.label }}</span><strong>{{ metric.value }}</strong><small>{{ metric.sub }}</small></div></div>
      </section>
      <section class="panel-card models-card">
        <div class="benefit-toolbar">
          <a-input v-model="modelSearch" allow-clear placeholder="搜索模型 ID / 名称 / 描述 / 厂商" aria-label="搜索模型"><template #prefix><icon-search /></template></a-input>
          <a-select v-if="supplier === 'auto'" v-model="modelRealm" style="width: 140px" aria-label="按平台筛选"><a-option value="">全部平台</a-option><a-option value="cn">WorkBuddy CN</a-option><a-option value="global">WorkBuddy Global</a-option><a-option value="loomy">Loomy</a-option><a-option value="minimax">MiniMax</a-option><a-option value="qoder">Qoder</a-option><a-option value="trae">Trae</a-option><a-option value="antigravity">Antigravity</a-option><a-option value="doubao">豆包</a-option></a-select>
          <a-select v-model="modelSort" style="width: 160px" aria-label="排序方式"><a-option value="default">上游默认顺序</a-option><a-option value="credits">积分倍率低→高</a-option><a-option value="context">上下文大→小</a-option><a-option value="output">最大输出大→小</a-option><a-option value="name">模型 ID A→Z</a-option></a-select>
        </div>
        <a-scrollbar class="table-scroll" outer-class="table-wrap"><table>
          <thead><tr><th>模型</th><th>积分倍率</th><th>默认档</th><th>支持档位</th><th>上下文</th><th>最大输出（实测）</th><th>能力</th></tr></thead>
          <tbody>
            <tr v-for="row in modelRows" :key="row.id">
              <td class="mono">{{ row.id }}<small v-if="row.name" class="benefit-detail">{{ row.name }}<template v-if="row.vendor"> · {{ row.vendor }}</template></small><small v-if="row.description" class="benefit-detail">{{ row.description }}</small></td>
              <td><a-tooltip :content="row.promo_note || '牌价'"><span v-if="rateCell(row).html" v-html="rateCell(row).value" /><span v-else>{{ rateCell(row).value }}</span></a-tooltip></td>
              <td><span class="schedule-chip" v-if="row.default_effort">{{ row.default_effort }}</span><span v-else>--</span></td>
              <td><div class="schedule-chips"><span v-for="effort in (row.supported_efforts || [])" :key="effort" class="schedule-chip">{{ effort }}</span></div></td>
              <td>{{ row.context_length ? formatNumber(row.context_length) : '--' }}</td>
              <td><span :class="probeCell(row).kind" :title="probeCell(row).tip">{{ probeCell(row).text }}</span></td>
              <td><a-tag v-for="tag in capabilityTags(row)" :key="tag" size="small">{{ tag }}</a-tag><span v-if="!capabilityTags(row).length">--</span></td>
            </tr>
          </tbody>
        </table></a-scrollbar><a-empty v-if="!modelRows.length" description="暂无模型（需至少一个可用账号）" />
        <footer class="ledger-foot"><span>实测上限来自 scripts/probe_max_tokens.py 的探测结果（/panel/api/model_probes）</span><span>共 {{ modelRows.length }} 个模型</span></footer>
      </section>
    </template>

    <template v-else-if="section === 'requests'">
      <section class="panel-card requests-summary">
        <div class="status-metrics"><div v-for="metric in requestSummary" :key="metric.label" class="status-metric"><span>{{ metric.label }}</span><strong>{{ metric.value }}</strong><small>{{ metric.sub }}</small></div></div>
      </section>
      <section class="panel-card requests-card">
        <div class="benefit-toolbar">
          <a-input v-model="reqQuery" allow-clear placeholder="搜索模型 / 账号 / IP / UA / 请求 ID" aria-label="搜索请求记录"><template #prefix><icon-search /></template></a-input>
          <a-select v-model="reqOutcome" style="width: 140px" aria-label="按结果筛选" @change="load('requests')"><a-option value="">全部结果</a-option><a-option value="success">成功</a-option><a-option value="http_error">HTTP 错误</a-option><a-option value="stream_error">流错误</a-option><a-option value="interrupted">中断</a-option></a-select>
          <a-select v-model="reqRange" style="width: 130px" aria-label="时间范围" @change="load('requests')"><a-option :value="0">全部（限条数）</a-option><a-option :value="86400">最近 24 小时</a-option><a-option :value="604800">最近 7 天</a-option></a-select>
          <a-select v-model="reqLimit" style="width: 120px" aria-label="读取条数" @change="load('requests')"><a-option :value="100">最近 100 条</a-option><a-option :value="300">最近 300 条</a-option><a-option :value="1000">最近 1000 条</a-option></a-select>
          <a-button class="csv-export" :disabled="!requestRows.length" @click="exportRequests">导出 CSV</a-button>
        </div>
        <a-scrollbar class="table-scroll" outer-class="table-wrap"><table>
          <thead><tr><th>时间</th><th>结果</th><th>模型</th><th>账号</th><th>来源</th><th>耗时</th><th>Tokens</th><th>积分</th><th>请求 ID</th></tr></thead>
          <tbody>
            <tr v-for="row in requestRows" :key="row.request_id">
              <td>{{ new Date(row.time).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) }}</td>
              <td><span :class="['type-pill', row.outcome === 'ok' ? 'income' : 'expense']">{{ outcomeName(row) }}<template v-if="row.status"> · {{ row.status }}</template></span></td>
              <td class="mono">{{ row.model || row.path }}</td>
              <td>{{ row.account || '--' }}<small v-if="row.path && row.model" class="benefit-detail">{{ row.path }}</small></td>
              <td class="mono">{{ row.client_ip || '--' }}<small v-if="row.user_agent" class="benefit-detail">{{ row.user_agent }}</small></td>
              <td>{{ row.duration_ms }}ms<small v-if="row.ttfb_ms" class="benefit-detail">TTFB {{ row.ttfb_ms }}ms</small></td>
              <td>{{ row.total_tokens ? formatNumber(row.total_tokens) : '--' }}</td>
              <td>{{ row.credit_known ? row.credit : '--' }}</td>
              <td class="mono request-id"><span class="request-id-text">{{ row.request_id }}</span><a-button type="text" size="mini" aria-label="复制请求 ID" @click="copyText(row.request_id, '请求 ID 已复制')"><template #icon><icon-copy /></template></a-button></td>
            </tr>
          </tbody>
        </table></a-scrollbar><a-empty v-if="!requestRows.length" description="暂无请求记录" />
        <footer class="ledger-foot"><span>归档 {{ archiveState?.enabled ? '开启 · 保留 ' + (archiveState.retention_days ?? '?') + ' 天' : '关闭（仅内存指标）' }}</span><span>共 {{ requestRows.length }} 条</span></footer>
      </section>
    </template>

    <section v-else class="gateway-settings settings-layout">
      <aside class="billing-side settings-nav-col">
        <section class="settings-nav-panel">
          <h2>设置分组</h2>
          <nav class="settings-nav" aria-label="设置分组">
            <button v-for="group in settingsGroups" :key="group.id" :class="{ active: activeSection === group.id }" :aria-current="activeSection === group.id ? 'true' : undefined" aria-controls="gateway-settings-form" @click="jumpToGroup(group.id)"><component :is="group.icon" /><span>{{ group.name }}</span></button>
          </nav>
        </section>
        <section class="settings-overview-panel">
          <h2>配置概览</h2>
          <div class="overview-rows"><div v-for="row in configOverview" :key="row.row" class="overview-row"><span>{{ row.row }}</span><b :class="{ 'accent-text': row.accent }">{{ row.value }}</b></div></div>
          <a-alert v-if="restartRequired.length" type="warning" class="restart-notice">{{ restartRequired.length }} 项需重启：{{ restartRequired.join('、') }}</a-alert>
          <footer class="settings-config-foot"><span class="mono">config.json</span><small v-if="savedAt">最后保存 {{ savedAt }}</small><a href="/panel/classic/" target="_blank" rel="noreferrer">查看原始配置 <icon-link /></a></footer>
        </section>
      </aside>

      <div id="gateway-settings-form" class="settings-panel-col" :aria-busy="loading">
        <template v-if="form">
          <header class="panel-head">
            <h2>{{ activeSettingsGroup.name }}</h2><span class="mono">config.json<template v-if="savedAt"> · {{ savedAt }}</template></span>
          </header>
          <div class="settings-body">
          <a-alert v-if="restartRequired.length || ['access', 'upstream', 'prompt', 'models'].includes(activeSection)" type="warning" class="settings-restart-info"><template v-if="restartRequired.length">以下字段需重启进程后生效：{{ restartRequired.join('、') }}</template><template v-else>标注「需重启」的字段保存后需重启进程生效。</template></a-alert>
          <section v-show="activeSection === 'access'" id="settings-access" class="settings-section">
            <header class="ledger-head"><h3>接入</h3></header>
            <div class="settings-grid">
              <label><span>监听地址 listen <em class="restart-badge">需重启</em></span><a-input v-model="form.listen" placeholder=":7863" /></label>
              <label><span>管理密钥 api_key</span><a-input-password v-model="form.api_key" placeholder="留空不鉴权" :input-attrs="{ autocomplete: 'new-password' }" /></label>
              <label><span>入站读取超时 <em class="restart-badge">需重启</em></span><a-input v-model="form.server.read_timeout" placeholder="300s；0 不限制" /></label>
            </div>
          </section>

          <section v-show="activeSection === 'schedule'" id="settings-schedule" class="settings-section">
            <header class="ledger-head"><div class="ledger-title"><h2>定时任务</h2><small>签到时点在「签到设置」页维护；启停即时热生效</small></div></header>
            <div class="settings-grid">
              <label class="inline-between">Token 保活<a-switch v-model="form.schedule.keepalive_enabled" /></label>
              <label>保活时点（小时）<a-input v-model="form.schedule.keepalive_hours" placeholder="22" /></label>
              <label class="inline-between">猫猫旅行<a-switch v-model="form.schedule.travel_enabled" /></label>
              <label>旅行时点（小时）<a-input v-model="form.schedule.travel_hours" placeholder="9, 21" /></label>
              <label class="inline-between">活跃上报<a-switch v-model="form.schedule.activity_enabled" /></label>
              <label>上报时点（小时）<a-input v-model="form.schedule.activity_hours" placeholder="10" /></label>
              <label class="inline-between">成长任务自动执行<a-switch v-model="form.schedule.growth_enabled" /></label>
              <label>执行时点（小时）<a-input v-model="form.schedule.growth_hours" placeholder="1" /></label>
              <label class="inline-between">连登管家（自动兑换/抽奖）<a-switch v-model="form.schedule.streak_enabled" /></label>
              <label class="inline-between">任务补领清扫<a-switch v-model="form.schedule.task_sweep_enabled" /></label>
              <label>清扫间隔（分钟）<a-input v-model="form.schedule.task_sweep_minutes" type="number" min="1" /></label>
              <label class="inline-between">后台刷新余额<a-switch v-model="form.schedule.balance_refresh_enabled" /></label>
              <label>刷新间隔（分钟）<a-input v-model="form.schedule.balance_refresh_minutes" type="number" min="1" /></label>
              <label class="inline-between">保号任务覆盖已禁用账号<a-switch v-model="form.schedule.include_disabled_in_tasks" /></label>
            </div>
          </section>

          <section v-show="activeSection === 'pool'" id="settings-pool" class="settings-section">
            <header class="ledger-head"><div class="ledger-title"><h2>选号与流量治理</h2><small>双域策略、熔断冷却与权重</small></div></header>
            <div class="settings-grid">
              <label>国内版调度策略<a-select v-model="form.pool.strategy_domestic"><a-option value="single_account">单账号耗尽</a-option><a-option value="weighted_random">加权随机</a-option><a-option value="round_robin">轮询</a-option><a-option value="sequential_exhaust">顺序耗尽</a-option></a-select></label>
              <label>国际版调度策略<a-select v-model="form.pool.strategy_international"><a-option value="single_account">单账号耗尽</a-option><a-option value="weighted_random">加权随机</a-option><a-option value="round_robin">轮询</a-option><a-option value="sequential_exhaust">顺序耗尽</a-option></a-select></label>
              <label>国内版账号顺序（UID，逗号分隔）<a-input v-model="form.pool.order_domestic" placeholder="uid1, uid2" /></label>
              <label>国际版账号顺序（UID）<a-input v-model="form.pool.order_international" placeholder="uid1, uid2" /></label>
              <label>单账号最大在途（0 = 不限）<a-input v-model="form.pool.max_in_flight" type="number" min="0" /></label>
              <label>国际版在途上限<a-input v-model="form.pool.max_in_flight_global" type="number" min="1" /></label>
              <label>连续失败熔断阈值<a-input v-model="form.pool.breaker_threshold" type="number" min="1" /></label>
              <label>熔断基础时长<a-input v-model="form.pool.breaker_cooldown" placeholder="30m" /></label>
              <label>熔断退避上限<a-input v-model="form.pool.breaker_cooldown_max" placeholder="6h" /></label>
              <label>软限流冷却基数<a-input v-model="form.cooldown.soft_rate" placeholder="600s" /></label>
              <label>软冷却退避上限<a-input v-model="form.cooldown.soft_rate_max" placeholder="2h" /></label>
              <label>连败降权阈值<a-input v-model="form.pool.degrade_threshold" type="number" min="1" /></label>
              <label>连败降权时长<a-input v-model="form.pool.degrade_cooldown" placeholder="10m" /></label>
              <label>连败降权上限<a-input v-model="form.pool.degrade_cooldown_max" placeholder="2h" /></label>
              <label>闲置补偿 / 小时<a-input v-model="form.pool.idle_weight_per_hour" type="number" step="0.1" /></label>
              <label>闲置补偿上限<a-input v-model="form.pool.idle_weight_max" type="number" step="0.1" /></label>
              <label>成本探索窗口（0 关停）<a-input v-model="form.pool.cost_explore_interval" placeholder="30m" /></label>
              <label>积分保底（0 关闭）<a-input v-model="form.pool.credit_floor" type="number" min="0" /></label>
              <label>快过期路由窗口（0 关闭）<a-input v-model="form.pool.expiring_soon" placeholder="168h" /></label>
              <label class="inline-between">快过期积分优先<a-switch v-model="form.pool.prefer_expiring" /></label>
              <label class="inline-between">裸模型名跨域回退<a-switch v-model="form.pool.cross_domain_fallback" /></label>
            </div>
          </section>

          <section v-show="['access', 'upstream'].includes(activeSection)" class="settings-section">
            <header class="ledger-head"><h3>上游参数 <em class="restart-badge">需重启</em></h3></header>
            <div class="settings-grid settings-pairs">
              <label>短请求超时（秒）<a-input v-model="form.upstream.timeout_seconds" type="number" min="1" /></label>
              <label>首字节超时（秒）<a-input v-model="form.upstream.header_timeout_seconds" type="number" min="1" /></label>
              <label>流空闲超时（秒）<a-input v-model="form.upstream.idle_timeout_seconds" type="number" min="1" /></label>
              <label>出站 User-Agent<a-input v-model="form.upstream.user_agent" placeholder="留空使用内置默认" /></label>
            </div>
          </section>

          <section v-show="activeSection === 'upstream'" id="settings-upstream" class="settings-section">
            <header class="ledger-head"><h3>会话路由</h3></header>
            <div class="settings-grid settings-pairs">
              <label>会话粘性 TTL（需重启）<a-input v-model="form.session_sticky.ttl" placeholder="30m" /></label>
              <label>会话 GC 周期（需重启）<a-input v-model="form.session_sticky.gc_interval" placeholder="5m" /></label>
            </div>
          </section>

          <section v-show="activeSection === 'prompt'" id="settings-prompt" class="settings-section">
            <div class="settings-grid settings-pairs">
              <label>系统提示词模式（需重启）<a-select v-model="form.prompt.mode"><a-option value="custom">custom · 网关自有提示词</a-option><a-option value="append">append · 客户端 system 后插入</a-option><a-option value="passthrough">passthrough · 透传</a-option></a-select></label>
              <label>提示词文件路径（需重启）<a-input v-model="form.prompt.file" placeholder="留空使用内置默认" /></label>
            </div>
          </section>

          <section v-show="activeSection === 'models'" id="settings-models" class="settings-section">
            <header class="ledger-head"><h3>模型黑名单</h3></header>
            <div class="settings-grid settings-wide">
              <label>模型黑名单（每行一个；裸名 = 两域，cn:xxx / global:xxx = 单域）<a-textarea v-model="form.models.hidden" :auto-size="{ minRows: 2, maxRows: 6 }" placeholder="cn:glm-5.2" /></label>
            </div>
          </section>

          <section v-show="['access', 'upstream', 'models'].includes(activeSection)" class="settings-section">
            <header class="ledger-head"><h3>{{ activeSection === 'models' ? '日志开关' : '安全与开关' }}</h3></header>
            <div class="settings-toggle-list">
              <label v-show="activeSection !== 'models'" class="inline-between"><span>会话粘性路由</span><a-switch v-model="form.session_sticky.enabled" aria-label="会话粘性路由" /></label>
              <label v-show="activeSection !== 'models'" class="inline-between"><span>出站请求指纹脱敏</span><a-switch v-model="form.features.sanitize_blacklist_fingerprints" aria-label="出站请求指纹脱敏" /></label>
              <label v-show="activeSection !== 'upstream'" class="inline-between"><span>记录调用来源</span><a-switch v-model="form.logging.request_client_info" aria-label="记录调用来源" /></label>
              <label v-show="activeSection !== 'upstream'" class="inline-between"><span>请求归档</span><a-switch v-model="form.logging.request_archive_enabled" aria-label="请求归档" /></label>
            </div>
          </section>

          <section v-show="['access', 'models'].includes(activeSection)" class="settings-section">
            <header class="ledger-head"><h3>日志与归档 <em class="restart-badge">需重启</em></h3></header>
            <div class="settings-grid settings-pairs">
              <label>归档保留天数<a-input v-model="form.logging.request_retention_days" type="number" min="1" /></label>
              <label>归档容量上限（MiB）<a-input v-model="form.logging.request_archive_max_mb" type="number" min="1" /></label>
            </div>
          </section>

          </div>
          <footer class="save-bar">
            <span class="settings-hint"><icon-thunderbolt />改动即时热生效，标注「需重启」的字段除外</span>
            <div class="modal-actions"><a-button type="text" :disabled="saving" @click="resetSettings">重置</a-button><a-button type="primary" :loading="saving" @click="saveSettings"><template #icon><icon-save /></template>保存设置</a-button></div>
          </footer>
        </template>
        <a-empty v-else :description="loading ? '正在加载设置' : '设置加载失败'"><a-button v-if="!loading" @click="load('settings')">重新加载</a-button></a-empty>
      </div>
    </section>
  </section>
</template>

<style scoped>
.gateway-usage-active { gap: 18px; }
.gateway-usage-active > .page-heading { margin: 0; }
.gateway-usage-active .eyebrow { font-size: 10px; line-height: 14px; }
.gateway-usage-active h1 { line-height: 42px; margin: 5px 0 4px; }
.gateway-usage-active .gateway-subline { flex-wrap: wrap; line-height: 19px; }
.gateway-usage-active .panel-card { min-width: 0; }
.gateway-usage-active h2 { margin: 0; font-size: 14px; }
.gateway-status.panel-card { display: block; padding: 0; overflow: hidden; }
.gateway-status .status-top { min-height: 54px; padding: 10px 18px; flex-wrap: nowrap; }
.gateway-status .status-left { flex-wrap: nowrap; }
.gateway-status .status-halo { box-shadow: none; }
.gateway-status .status-text { flex-shrink: 0; }
.gateway-status .status-text small { display: block; line-height: 17px; }
.gateway-status .endpoint-chips { flex-wrap: nowrap; }
.gateway-status .endpoint-chip { white-space: nowrap; }
.gateway-status .endpoint-chip b.get { color: var(--accent); }
.gateway-status .status-right { flex-shrink: 0; }
.gateway-status .key-state { gap: 6px; padding: 5px 10px; white-space: nowrap; }
.gateway-status .copy-primary { height: 32px; }
.gateway-status .status-metrics { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); min-height: 50px; padding: 7px 18px; border: 0; gap: 0; }
.gateway-status .status-metric { display: block; padding: 0 12px; border-left: 1px solid var(--line); }
.gateway-status .status-metric:first-child { padding-left: 0; border: 0; }
.gateway-status .status-metric > span { display: block; line-height: 14px; }
.metric-value { display: flex; align-items: baseline; gap: 5px; flex-wrap: wrap; }
.gateway-status .metric-value strong { line-height: 22px; }
.gateway-status .metric-value small { line-height: 14px; overflow-wrap: anywhere; }
.gateway-status .metric-warning :is(strong, small) { color: var(--warning); }
.usage-row, .gateway-usage-active .insights-row { display: grid; grid-template-columns: minmax(0, 1fr) 340px; gap: 18px; align-items: stretch; }
.usage-trend.panel-card { display: flex; flex-direction: column; gap: 10px; padding: 18px; min-height: 384px; }
.usage-trend > .ledger-head { margin: 0; min-height: 36px; flex-wrap: nowrap; }
.usage-trend .ledger-title h2 { line-height: 20px; }
.usage-trend .ledger-title small { font-size: 10.5px; line-height: 14px; margin-top: 2px; }
.usage-windows.filter-tabs { padding: 0; gap: 2px; background: transparent; border: 0; flex-shrink: 0; }
.usage-windows button { height: 26px; min-width: 35px; padding: 0 7px; color: var(--muted); font-size: 10.5px; }
.usage-windows button.active { color: var(--accent); }
.usage-trend .chart-kpis { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); min-height: 54px; margin: 0; gap: 0; }
.usage-trend .chart-kpi { padding: 0 12px; border-left: 1px solid var(--line); }
.usage-trend .chart-kpi:first-child { padding-left: 0; border: 0; }
.usage-trend .chart-kpi > span { line-height: 14px; }
.usage-trend .chart-kpi strong { font-size: 19px; line-height: 24px; }
.usage-trend .chart-kpi small { font-size: 9.5px; color: var(--muted); line-height: 14px; overflow-wrap: anywhere; }
.usage-trend .usage-chart { padding: 0; border: 0; border-radius: 0; background: transparent; margin-top: auto; }
.usage-trend .chart-row { gap: 8px; }
.usage-trend .chart-yaxis { width: 30px; height: 190px; flex-shrink: 0; padding: 0; }
.usage-trend .chart-yaxis span { font-family: Consolas, monospace; font-size: 8px; }
.usage-trend .chart-plot { height: 190px; min-width: 0; border: 0; border-radius: 0; background: transparent; }
.usage-trend .chart-grid, .usage-trend .chart-bars { inset: 0; }
.usage-trend .chart-grid i { border-top-style: solid; }
.usage-trend .chart-grid i:first-child { border-top-color: var(--line); }
.usage-trend .chart-bars { gap: 5px; justify-content: space-around; }
.usage-trend .usage-bar { min-width: 0; max-width: 18px; gap: 0; }
.usage-trend .usage-bar .seg { flex-shrink: 0; }
.usage-trend .usage-bar .seg.completion { background: #a6b2ff; opacity: 1; border-radius: 3px 3px 0 0; box-shadow: inset 0 -2px var(--surface); }
.usage-trend .usage-bar .seg.prompt { border-radius: 0; }
.usage-trend .usage-bar { cursor: default; outline: none; }
.usage-trend .usage-bar.hovered .seg { filter: brightness(1.15); }
.usage-trend .usage-bar:focus-visible { box-shadow: 0 0 0 2px var(--accent); border-radius: 3px; }
.chart-tip { position: absolute; z-index: 6; display: flex; flex-direction: column; gap: 2px; min-width: 104px; padding: 7px 10px; border-radius: 8px; background: #15161a; color: #eef0f5; font-size: 10.5px; line-height: 1.55; box-shadow: 0 8px 22px rgba(15, 17, 24, .24); pointer-events: none; white-space: nowrap; }
.chart-tip strong { font-size: 11px; font-weight: 500; color: #fff; }
.chart-tip span { display: flex; align-items: center; gap: 5px; color: #c9cddb; }
.chart-tip em { font-style: normal; color: #949bad; }
.chart-tip .usage-swatch { width: 6px; height: 6px; margin: 0; border-radius: 2px; }
.chart-tip .usage-swatch.prompt { background: var(--accent); }
.chart-tip .usage-swatch.completion { background: #a6b2ff; }
.usage-trend .chart-xlabels { padding: 8px 0 0 38px; gap: 5px; border: 0; border-radius: 0; background: transparent; }
.usage-trend .chart-xlabels span { flex: 1 1 0; min-width: 0; height: 14px; font-family: Consolas, monospace; font-size: 8px; white-space: nowrap; overflow: visible; }
.usage-trend .chart-foot { padding-top: 10px; gap: 10px; font-size: 10px; color: var(--muted); line-height: 14px; }
.chart-peak { display: flex; align-items: center; gap: 5px; }
.chart-peak .arco-icon { color: var(--accent); flex-shrink: 0; }
.usage-trend .usage-chart-legend { gap: 5px; font-size: 10px; flex-shrink: 0; }
.usage-trend .usage-swatch { width: 7px; height: 7px; margin: 0 0 0 8px; }
.usage-trend .usage-swatch.completion { background: #a6b2ff; }
.live-card { min-width: 0; height: 384px; display: flex; flex-direction: column; overflow: hidden; background: #15161a; border-radius: 14px; color: #d8dbe2; }
.live-head { min-height: 46px; padding: 0 14px; display: flex; justify-content: space-between; align-items: center; background: #1a1c22; }
.live-head > div { display: flex; align-items: center; gap: 7px; }
.gateway-usage-active .live-head h2 { color: #eeeff2; font-size: 12px; font-weight: 500; }
.live-head > span { color: #6b6e78; font-family: Consolas, monospace; font-size: 9px; }
.live-dot { display: inline-block; width: 5px; height: 5px; border-radius: 50%; background: #7c9cff; flex-shrink: 0; }
.live-badge { border-radius: 4px; padding: 2px 7px; background: #252a3c; color: #a6b2ff; font-family: Consolas, monospace; font-size: 8px; }
.live-card .live-feed-wrap { flex: 1; min-height: 0; }
.live-card :deep(.arco-scrollbar-container.live-feed) { display: block; height: 100%; max-height: none; overflow: auto; padding: 8px 14px; }
.live-card :deep(.arco-scrollbar.arco-scrollbar-type-embed .arco-scrollbar-thumb) { opacity: 0.55; }
.live-card :deep(.arco-scrollbar.arco-scrollbar-type-embed:hover .arco-scrollbar-thumb), .live-card :deep(.arco-scrollbar.arco-scrollbar-type-embed .arco-scrollbar-thumb-dragging) { opacity: 1; }
.live-card :deep(.arco-scrollbar-thumb-direction-vertical .arco-scrollbar-thumb-bar) { width: 4px; margin: 0 auto; border-radius: 2px; background: #454a58; }
.live-card :deep(.arco-scrollbar-thumb:hover .arco-scrollbar-thumb-bar), .live-card :deep(.arco-scrollbar-thumb-dragging .arco-scrollbar-thumb-bar) { background: #6b7080; }
.live-card .feed-row { min-height: 40px; padding: 6px 0; background: transparent; border: 0; border-bottom: 1px solid #23252c; border-radius: 0; gap: 2px; }
.live-card .feed-line1 { align-items: center; font-size: 10px; line-height: 13px; }
.feed-model { display: flex; align-items: center; gap: 7px; min-width: 0; }
.feed-model .mono { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #d8dbe2; font-size: 10px; }
.live-card .feed-line1 small { color: #8a8d97; font-family: Consolas, monospace; font-size: 9px; flex-shrink: 0; }
.live-card .feed-line2 { justify-content: flex-start; gap: 6px; padding-left: 12px; font-size: 9px; color: #8a8d97; line-height: 11px; }
.feed-line2 time { color: #6b6e78; font-family: Consolas, monospace; font-size: 8.5px; flex-shrink: 0; }
.feed-line2 > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.live-card .feed-line2 small { font-size: 9px; color: #6b6e78; flex-shrink: 0; }
.live-card .feed-error .live-dot { background: #e1b56c; }
.live-card .feed-error .feed-outcome, .live-card .feed-error .feed-line1 small { color: #e1b56c; }
.live-foot { display: flex; justify-content: space-between; align-items: center; gap: 8px; min-height: 40px; padding: 0 14px; border-top: 1px solid #262a36; }
.live-foot > span { display: flex; align-items: center; gap: 6px; color: #979aa4; font-size: 9px; }
.live-foot button { display: inline-flex; align-items: center; gap: 6px; padding: 0; background: transparent; border: 0; color: #a6b2ff; font-size: 9px; white-space: nowrap; }
.gateway-usage-active .insights-row > .panel-card { display: flex; flex-direction: column; gap: 8px; min-height: 320px; padding: 16px; }
.gateway-usage-active .insights-row .ledger-head { min-height: 30px; margin: 0; }
.gateway-usage-active .insights-row .ledger-title.inline { display: flex; gap: 8px; flex-wrap: wrap; }
.model-usage-card .table-wrap { min-width: 0; max-height: 248px; flex: 1; overflow: auto; }
.model-usage-card table { table-layout: fixed; font-size: 10.5px; }
.model-usage-card th { height: 28px; padding: 4px 0; font-size: 10px; }
.model-usage-card td { height: 44px; vertical-align: middle; padding: 6px 8px 6px 0; color: var(--secondary); }
.model-usage-card :is(th, td):first-child { width: 39%; }
.model-usage-card :is(th, td):nth-child(2) { width: 7.5%; }
.model-usage-card :is(th, td):nth-child(3) { width: 28.5%; }
.model-usage-card :is(th, td):nth-child(n+4) { width: 8%; }
.model-usage-card td strong { color: var(--ink); font-size: 11px; font-weight: 500; overflow-wrap: anywhere; }
.model-usage-card .benefit-detail { font-size: 9px; }
.model-usage-card .token-bar { gap: 9px; }
.token-track { display: block; width: 112px; max-width: 58%; height: 4px; border-radius: 2px; background: var(--line); }
.model-usage-card .token-track i { max-width: 100%; height: 4px; }
.model-usage-card .token-bar > span:last-child { font-size: 10.5px; white-space: nowrap; }
.runtime-card .overview-rows { display: grid; flex: 1; }
.runtime-card .overview-row { min-height: 42px; align-items: center; padding: 10px 0; font-size: 10.5px; }
.runtime-card .overview-row:last-child { border-bottom: 1px solid var(--line); }
.runtime-card .overview-row b { font-weight: 500; }
.runtime-card .key-state { font-size: 9px; padding: 3px 7px; border-radius: 5px; }
.runtime-foot { display: flex; justify-content: space-between; align-items: center; gap: 8px; min-height: 28px; color: var(--muted); font-size: 9.5px; }
@media (max-width: 1400px) {
  .gateway-status .status-top { flex-wrap: wrap; }
  .gateway-status .status-left { flex-wrap: wrap; gap: 10px; }
}
@media (max-width: 1200px) {
  .usage-row, .gateway-usage-active .insights-row { grid-template-columns: minmax(0, 1fr); }
  .live-card { height: 384px; }
  .gateway-status .status-metrics { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px 0; }
  .gateway-status .status-metric:nth-child(4) { padding-left: 0; border-left: 0; }
}
@media (max-width: 600px) {
  .gateway-status .status-top { padding: 14px; gap: 12px; }
  .gateway-status .status-left { gap: 10px; }
  .gateway-status .status-left > .status-vdivider { display: none; }
  .gateway-status .endpoint-chips { width: 100%; flex-wrap: wrap; gap: 5px; }
  .gateway-status .status-metrics { padding: 12px 14px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .gateway-status .status-metric:nth-child(4) { padding-left: 12px; border-left: 1px solid var(--line); }
  .gateway-status .status-metric:nth-child(odd) { padding-left: 0; border-left: 0; }
  .usage-trend.panel-card { padding: 14px; }
  .usage-trend > .ledger-head { flex-wrap: wrap; gap: 10px; }
  .usage-trend .chart-kpis { gap: 12px 0; }
  .usage-trend .chart-kpi { padding: 0 7px; }
  .usage-trend .chart-kpi strong { font-size: 17px; }
  .usage-trend .chart-kpi small { font-size: 9px; }
  .usage-trend .chart-bars, .usage-trend .chart-xlabels { gap: 2px; }
  .usage-trend .chart-foot { flex-wrap: wrap; }
  .usage-trend .usage-chart-legend { margin-left: auto; }
  .model-usage-card table { min-width: 620px; }
}
.gateway-settings-active > .page-heading { margin-bottom: 10px; }
.gateway-settings.settings-layout { display: grid; grid-template-columns: 248px minmax(0, 1fr); gap: 18px; align-items: start; }
.settings-nav-col { display: grid; grid-template-columns: 1fr; gap: 18px; }
.settings-nav-panel, .settings-overview-panel, .settings-panel-col { background: var(--surface); border: 1px solid var(--line); border-radius: 14px; }
.settings-nav-panel { padding: 12px; }
.gateway-settings h2 { margin: 0; font-size: 16px; }
.settings-nav-col h2 { color: var(--muted); font-size: 11px; font-weight: 400; margin: 0 0 6px; }
.settings-nav { gap: 4px; }
.settings-nav button { justify-content: flex-start; gap: 10px; min-height: 42px; padding: 10px; border: 0; border-radius: 8px; font-size: 13px; text-align: left; }
.settings-nav button .arco-icon { width: 15px; height: 15px; flex-shrink: 0; color: var(--muted); }
.settings-nav button.active { font-weight: 600; }
.settings-nav button.active .arco-icon { color: var(--accent); }
.settings-nav button:hover { background: var(--surface-secondary); }
.settings-nav button.active:hover { background: var(--accent-soft); }
.settings-nav button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.settings-overview-panel { padding: 14px; }
.overview-row { align-items: center; min-height: 38px; padding: 9px 0; font-size: 11px; }
.overview-row b { font-weight: 500; }
.settings-config-foot { display: grid; gap: 5px; padding-top: 12px; color: var(--secondary); font-size: 11px; }
.settings-config-foot small { color: var(--muted); }
.settings-config-foot a { display: flex; align-items: center; gap: 6px; color: var(--accent); }
.settings-panel-col { overflow: hidden; min-height: 858px; display: flex; flex-direction: column; container-type: inline-size; }
.panel-head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; min-height: 72px; padding: 18px 24px; }
.panel-head > span { font-size: 10px; color: var(--muted); }
.settings-body { padding: 20px 24px 30px; flex: 1; }
.settings-restart-info { margin-bottom: 20px; border-radius: 8px; background: var(--warning-soft); color: var(--warning); font-size: 11px; }
.settings-section { padding: 0; border: 0; margin-bottom: 20px; }
.settings-section:last-of-type { padding: 0; margin-bottom: 0; }
.settings-section h2, .settings-section h3 { margin: 0; font-size: 13px; line-height: 1.6; color: var(--ink); }
.settings-section .ledger-head { margin-bottom: 12px; }
.settings-section .ledger-title small { display: none; }
.settings-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.settings-grid.settings-pairs { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.settings-grid.settings-wide { grid-template-columns: 1fr; }
.settings-grid label { gap: 7px; font-size: 11px; line-height: 1.6; overflow-wrap: anywhere; }
.settings-grid label.inline-between { border-bottom: 1px solid var(--line); min-height: 48px; gap: 12px; flex-wrap: nowrap; }
.settings-grid :deep(.arco-input-wrapper), .settings-grid :deep(.arco-select-view-single) { min-height: 38px; border-radius: 8px; }
.settings-grid :deep(.arco-input) { font-family: 'JetBrains Mono', Consolas, monospace; color: var(--ink); background: transparent; }
.settings-grid :deep(.arco-textarea) { color: var(--ink); background: var(--surface); }
.restart-badge { display: inline-block; padding: 1px 5px; border-radius: 4px; background: var(--warning-soft); color: var(--warning); font-size: 9px; font-style: normal; font-weight: 400; vertical-align: middle; }
.settings-toggle-list label { min-height: 44px; padding: 10px 0; border-bottom: 1px solid var(--line); color: var(--secondary); font-size: 12px; }
.settings-toggle-list :deep(.arco-switch-checked) { --switch-on: var(--accent); }
.save-bar { padding: 15px 24px; min-height: 64px; margin-top: auto; }
.save-bar .modal-actions { margin: 0; flex-shrink: 0; }
.settings-hint { display: flex; align-items: center; gap: 8px; font-size: 11px; }
.settings-hint .arco-icon { color: var(--accent); flex-shrink: 0; width: 14px; height: 14px; }
.restart-notice { margin-top: 12px; overflow-wrap: anywhere; }
@container (max-width: 700px) {
  .settings-body .settings-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .settings-body .settings-wide { grid-template-columns: 1fr; }
  .save-bar { flex-wrap: wrap; }
  .save-bar .modal-actions { margin-left: auto; }
}
@container (max-width: 430px) {
  .settings-body .settings-grid { grid-template-columns: 1fr; }
  .panel-head { padding: 16px; }
  .settings-body { padding: 16px; }
  .save-bar { padding: 14px 16px; }
}
@media (max-width: 1000px) {
  .gateway-settings.settings-layout { grid-template-columns: minmax(0, 1fr); }
  .settings-layout .settings-nav-col { grid-template-columns: 1fr; }
  .settings-nav { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .settings-overview-panel { display: none; }
  .settings-panel-col { min-height: 0; }
}
@media (max-width: 480px) {
  .settings-nav { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .settings-nav button { font-size: 12px; }
}
/* 模型 / 请求日志 表格适配可视宽度：固定布局按比例分列，长内容折行，不出现横向溢出 */
.models-card table, .requests-card table { table-layout: fixed; }
.models-card :is(th, td), .requests-card :is(th, td) { overflow-wrap: anywhere; }
.models-card :is(th, td):nth-child(1) { width: 30%; }
.models-card :is(th, td):nth-child(2) { width: 10%; }
.models-card :is(th, td):nth-child(3) { width: 8%; }
.models-card :is(th, td):nth-child(4) { width: 12%; }
.models-card :is(th, td):nth-child(5) { width: 9%; }
.models-card :is(th, td):nth-child(6) { width: 15%; }
.models-card :is(th, td):nth-child(7) { width: 16%; }
.requests-card :is(th, td):nth-child(1) { width: 11%; }
.requests-card :is(th, td):nth-child(2) { width: 9%; }
.requests-card :is(th, td):nth-child(3) { width: 13%; }
.requests-card :is(th, td):nth-child(4) { width: 11%; }
.requests-card :is(th, td):nth-child(5) { width: 23%; }
.requests-card :is(th, td):nth-child(6) { width: 8%; }
.requests-card :is(th, td):nth-child(7) { width: 6%; }
.requests-card :is(th, td):nth-child(8) { width: 5%; }
.requests-card :is(th, td):nth-child(9) { width: 14%; }
.requests-card .request-id { display: table-cell; max-width: none; white-space: nowrap; overflow-wrap: normal; }
.requests-card .request-id-text { display: inline-block; max-width: calc(100% - 32px); overflow: hidden; text-overflow: ellipsis; vertical-align: middle; }
.requests-card .request-id .arco-btn { vertical-align: middle; }
.models-card .schedule-chip { overflow-wrap: normal; }
/* 模型 / 请求日志：整页收进可视高度，表格在卡片内滚动，页面本身不出滚动条 */
.gateway-models-active, .gateway-requests-active { display: flex; flex-direction: column; height: calc(100dvh - 80px); min-height: 0; }
.gateway-models-active > .page-heading, .gateway-requests-active > .page-heading { flex-shrink: 0; }
.gateway-models-active > .panel-card, .gateway-requests-active > .panel-card { flex-shrink: 0; }
.gateway-models-active > .models-card, .gateway-requests-active > .requests-card { display: flex; flex-direction: column; flex: 1; min-height: 0; }
.models-card .table-wrap, .requests-card .table-wrap { flex: 1; min-height: 120px; }
.models-card .ledger-foot, .requests-card .ledger-foot { flex-shrink: 0; }
.table-wrap > :deep(.arco-scrollbar-container) { height: 100%; overflow: auto; }
</style>
