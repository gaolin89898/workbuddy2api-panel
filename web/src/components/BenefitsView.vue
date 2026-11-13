<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { Alert as AAlert, Button as AButton, Empty as AEmpty, Input as AInput, Modal as AModal, Option as AOption, Progress as AProgress, Radio as ARadio, RadioGroup as ARadioGroup, Select as ASelect, Spin as ASpin, Table as ATable, TableColumn as ATableColumn, Tag as ATag, Message } from '@arco-design/web-vue';
import { IconRefresh, IconPlayArrow, IconSearch, IconGift, IconCopy, IconCheck, IconClockCircle, IconArrowRight, IconInfoCircle, IconCalendar, IconThunderbolt } from '@arco-design/web-vue/es/icon';
import { accountCredits, formatNumber } from '../useAccounts';

const props = defineProps({ model: Object, section: String });
const uid = ref(''), loading = ref(false), working = ref(false), error = ref(''), result = ref('');
const tasks = ref([]), automatic = ref([]), rewards = ref(null);
const queue = ref({ running: false, items: [] }), scan = ref(null), concurrency = ref(1);
const filter = ref('all'), search = ref(''), confirmation = ref(null);
const loadedAt = ref(null), rulesView = ref(null);
let version = 0, queueTimer;
const abort = new AbortController();
const api = (path, options = {}) => props.model.api(path, { ...options, signal: abort.signal });
const supported = computed(() => props.model.provider === 'workbuddy' && props.model.platformSelection === 'workbuddy-cn');
const personal = computed(() => supported.value ? props.model.accounts.filter(a => !a.disabled && !a.enterprise && a.benefits_supported !== false) : []);
const unsupportedAccounts = computed(() => supported.value ? props.model.accounts.filter(a => a.benefits_supported === false) : []);
const options = personal;
const account = computed(() => options.value.find(a => a.uid === uid.value));
const workbuddyReady = computed(() => supported.value && props.model.accounts.length > 0);
const completedTasks = computed(() => tasks.value.filter(t => t.claimed).length);
const taskProgress = computed(() => tasks.value.length ? completedTasks.value / tasks.value.length : 0);
const availableTiers = computed(() => (rewards.value?.streak?.redemption_status?.tiers || []).filter(canRedeem));
const canCompensate = computed(() => rewards.value?.compensation?.active && !rewards.value.compensation.claimed && rewards.value.compensation.credit_num > 0);
const claimCount = computed(() => availableTiers.value.length + Number(!!canCompensate.value));
const rewardCredits = computed(() => availableTiers.value.reduce((sum, t) => sum + Number(t.credit || 0), 0) + (canCompensate.value ? Number(rewards.value.compensation.credit_num) : 0));
const scanAccounts = computed(() => {
  const uids = new Set(props.model.accounts.map(a => a.uid));
  return (scan.value?.accounts || []).filter(a => uids.has(a.uid));
});
const scanPending = computed(() => scanAccounts.value.reduce((sum, a) => sum + (a.growth || []).length, 0));
const coverage = computed(() => props.model.accounts.map(a => ({ ...a, pendingTasks: scanAccounts.value.find(item => item.uid === a.uid) })));
const taskRows = computed(() => tasks.value.filter(t => (!search.value || (t.title + ' ' + t.task_code).toLowerCase().includes(search.value.toLowerCase())) && (filter.value === 'all' || filter.value === 'claimable' && t.claimable || filter.value === 'claimed' && t.claimed || filter.value === 'pending' && !t.claimed && !t.claimable || filter.value === 'error' && queue.value.items?.some(i => i.uid === uid.value && i.code === t.task_code && i.status === 'error'))));
const done = computed(() => (queue.value.items || []).filter(i => ['done', 'skipped', 'error'].includes(i.status)).length);
const statusName = value => ({ pending: '等待执行', running: '执行中', done: '已完成', skipped: '已跳过', error: '失败', locked: '未解锁', claimed: '已领取', redeemed: '已兑换', available: '可兑换', unlocked: '可兑换', claimable: '可兑换' })[value] || value || '--';
const date = value => {
  if (!value) return '--';
  const instant = typeof value === 'string' && /^\d{4}-\d\d-\d\d /.test(value) ? value.replace(' ', 'T') + '+08:00' : value;
  const parsed = new Date(instant);
  return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false });
};
function tierStatus(tier) { return rewards.value?.streak?.redemption_status?.['tier_' + tier.tier + '_status']; }
function canRedeem(tier) { return tier.days > 0 && rewards.value?.streak?.streak?.days >= tier.days && !!tierStatus(tier) && !['locked', 'claimed', 'redeemed'].includes(tierStatus(tier)); }
function taskState(task) {
  if (task.claimed) return { label: '已完成', color: 'arcoblue' };
  const queued = queue.value.items?.find(item => item.uid === uid.value && item.code === task.task_code);
  if (queued?.status === 'running') return { label: '执行中', color: 'arcoblue' };
  if (queued?.status === 'error') return { label: '失败', color: 'red' };
  return { label: task.locked ? '未解锁' : task.claimable ? '待领取' : '待执行', color: task.claimable ? 'orange' : 'gray' };
}

async function load() {
  const request = ++version;
  const section = props.section, selected = uid.value;
  error.value = ''; result.value = ''; loading.value = true;
  tasks.value = []; automatic.value = []; rewards.value = null;
  try {
    if (selected) {
      const response = await api(section === 'tasks' ? `accounts/${encodeURIComponent(selected)}/tasks` : `benefits/accounts/${encodeURIComponent(selected)}/rewards`);
      if (request !== version) return;
      if (section === 'tasks') { tasks.value = response.tasks || []; automatic.value = response.automatable_codes || []; }
      else rewards.value = response;
    }
  } catch (cause) { if (request === version && cause.name !== 'AbortError') error.value = cause.message; }
  finally { if (request === version) { loading.value = false; loadedAt.value = new Date(); } }
}
async function refresh() {
  if (working.value || loading.value) return;
  if (props.model.provider === 'qoder') {
    const request = ++version;
    error.value = ''; result.value = ''; loading.value = true;
    try { await props.model.refreshOverview(); }
    catch (cause) { if (request === version && cause.name !== 'AbortError') error.value = cause.message; }
    finally { if (request === version) loading.value = false; }
    return;
  }
  await load();
}
watch([() => props.section, () => props.model.platformSelection], () => {
  uid.value = options.value[0]?.uid || '';
  scan.value = null; filter.value = 'all'; search.value = ''; confirmation.value = null;
  load();
}, { immediate: true });
watch(() => options.value.map(a => a.uid).join(','), () => {
  if (!account.value) { uid.value = options.value[0]?.uid || ''; load(); }
});
function selectAccount(value) { uid.value = value || ''; load(); }
async function pollQueue() {
  clearTimeout(queueTimer);
  if (!supported.value) return;
  try {
    const previous = queue.value.running;
    const response = await api('tasks/queue');
    if (!supported.value) return;
    queue.value = response;
    if (previous && !queue.value.running) { await props.model.refreshOverview(); if (props.section === 'tasks') await load(); }
    if (queue.value.running) queueTimer = setTimeout(pollQueue, 2000);
  } catch (cause) { if (supported.value && cause.name !== 'AbortError') { error.value = cause.message; queueTimer = setTimeout(pollQueue, 5000); } }
}
watch(supported, value => {
  clearTimeout(queueTimer);
  queue.value = { running: false, items: [] };
  if (value) pollQueue();
}, { immediate: true });
onBeforeUnmount(() => { ++version; clearTimeout(queueTimer); abort.abort(); });
async function scanTasks() {
  if (working.value || !supported.value || !personal.value.length) return;
  const platform = props.model.platformSelection;
  working.value = true;
  try {
    const response = await api('tasks/scan_all', { method: 'POST' });
    if (platform === props.model.platformSelection) scan.value = response;
  }
  catch (cause) { if (platform === props.model.platformSelection && cause.name !== 'AbortError') error.value = cause.message; }
  finally { working.value = false; }
}
async function execute(operation, label, payload = {}) {
  if (working.value || queue.value.running || !supported.value || (operation === 'queue' ? !personal.value.length : !account.value)) return;
  const selected = uid.value, section = props.section, platform = props.model.platformSelection;
  working.value = true; error.value = ''; confirmation.value = null;
  try {
    const response = operation === 'queue'
      ? await api('tasks/run_queue', { method: 'POST', body: JSON.stringify({ growth: true, concurrency: concurrency.value }) })
      : await api(section === 'tasks' ? `accounts/${encodeURIComponent(selected)}/tasks/${operation}` : `benefits/accounts/${encodeURIComponent(selected)}/rewards`, { method: 'POST', body: JSON.stringify(payload) });
    if (operation === 'queue') {
      await pollQueue();
      if (section === props.section && platform === props.model.platformSelection) result.value = response.message || '任务队列已启动';
      return;
    }
    if (section === 'tasks' && operation !== 'accept') {
      try { await api(`accounts/${encodeURIComponent(selected)}/balance`, { method: 'POST' }); }
      catch { Message.warning('操作已返回，但余额刷新失败，请稍后刷新积分'); }
    }
    await props.model.refreshOverview();
    if (section === props.section && selected === uid.value && platform === props.model.platformSelection) {
      await load();
      result.value = response.claim_error || response.claimable === false && operation === 'auto' && !response.claimed
        ? response.claim_error || response.message || '任务尚未确认完成，请稍后刷新'
        : response.message || (response.prize ? JSON.stringify(response.prize) : label + '成功');
    }
    if (response.claim_error || response.claimable === false && operation === 'auto' && !response.claimed) Message.warning(response.message || '任务尚未确认完成');
    else Message.success(response.message || label + '成功');
  } catch (cause) { if (section === props.section && platform === props.model.platformSelection && cause.name !== 'AbortError') error.value = cause.message; }
  finally { working.value = false; }
}
function confirmTask(task) { confirmation.value = task ? { title: '执行任务', task } : { title: '执行全部待办' }; }
function confirmExecution() { if (confirmation.value?.rewards) return claimRewards(); const task = confirmation.value?.task; execute(task ? 'auto' : 'queue', '执行', task ? { task_code: task.task_code } : {}); }
async function claimRewards() {
  if (working.value || queue.value.running || !account.value || !claimCount.value) return;
  const selected = uid.value, provider = props.model.provider;
  const actions = [...availableTiers.value.map(t => ({ action: 'redeem', tier: t.tier })), ...(canCompensate.value ? [{ action: 'compensation' }] : [])];
  working.value = true; confirmation.value = null; error.value = '';
  let claimed = 0;
  const failures = [];
  try {
    for (const action of actions) {
      try { await api(`benefits/accounts/${encodeURIComponent(selected)}/rewards`, { method: 'POST', body: JSON.stringify(action) }); claimed++; }
      catch (cause) { if (cause.name === 'AbortError') return; failures.push(cause.message); }
    }
    await props.model.refreshOverview();
    if (uid.value === selected && props.section === 'rewards' && props.model.provider === provider) {
      await load(); result.value = `已领取 ${claimed} 项奖励`; error.value = failures.join('；');
    }
  } catch (cause) { if (cause.name !== 'AbortError') error.value = cause.message; }
  finally { working.value = false; }
}
function jumpTo(element) { element?.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
async function copy(value) { try { await navigator.clipboard.writeText(value); Message.success('已复制券码'); } catch { Message.error('复制失败'); } }
// ── 网关能力补齐（经典控制台迁移）─────────────────────────────
const gateway = computed(() => !!props.model.capabilities?.gateway);
const gatewayWorking = ref('');
async function gatewayBatch(path, label) {
  if (gatewayWorking.value) return;
  gatewayWorking.value = path;
  error.value = ''; result.value = '';
  try {
    const data = await api(path, { method: 'POST' });
    if (path === 'compensation_all' && data.summary) {
      const s = data.summary;
      result.value = `全账号补偿领取完成：合计 ${s.total} · 领取 ${s.claimed}（+${formatNumber(s.credit)} 分）· 跳过 ${s.skipped} · 失败 ${s.errored}`;
      if (!s.ok && s.ok === false) Message.warning('部分账号未拿到确定结论，请看结果详情');
      else Message.success('补偿领取完成');
    } else Message.success(label + '已触发，进度看运行日志');
    await props.model.refreshOverview();
  } catch (cause) { if (cause.name !== 'AbortError') error.value = cause.message; }
  finally { gatewayWorking.value = ''; }
}
const packages = ref(null), packagesLoading = ref(false), expandedPackage = ref('');
const packageRows = computed(() => (packages.value?.accounts || []).filter(a => a.uid).map(account => {
  let earliest = Infinity;
  for (const pkg of account.packages || []) {
    if (pkg.remain > 0 && pkg.expires_at) {
      const timestamp = new Date(pkg.expires_at).getTime();
      if (timestamp < earliest) earliest = timestamp;
    }
  }
  return { ...account, earliestExpiry: Number.isFinite(earliest) ? expiryDate(earliest) : '--' };
}));
async function loadPackages() {
  if (!workbuddyReady.value) return;
  packagesLoading.value = true;
  try { const response = await api('packages'); if (workbuddyReady.value) packages.value = response; }
  catch (cause) { if (workbuddyReady.value && cause.name !== 'AbortError') error.value = cause.message; }
  finally { packagesLoading.value = false; }
}
function expiryDate(value) {
  if (!value) return '--';
  const parsed = new Date(typeof value === 'number' ? value : String(value).replace(' ', 'T'));
  return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
}
watch([() => props.section, workbuddyReady], ([section, ready]) => { if (section === 'rewards' && ready) loadPackages(); }, { immediate: true });
</script>

<template>
  <section :class="['benefits-view', 'benefits-' + section]">
    <header class="page-heading benefit-heading">
      <div class="page-toolbar"><slot name="platform-switcher" /><div class="heading-actions">
        <template v-if="section === 'tasks' && supported">
          <a-button :loading="working" :disabled="queue.running || !personal.length" @click="scanTasks"><template #icon><icon-search /></template>扫描全部待办</a-button>
          <a-button type="primary" :disabled="working || queue.running || !personal.length" @click="confirmTask(null)"><template #icon><icon-play-arrow /></template>执行全部待办</a-button>
        </template>
        <template v-else>
          <a-button v-if="supported || model.provider === 'qoder'" :loading="loading" :disabled="working" @click="refresh"><template #icon><icon-refresh /></template>刷新状态</a-button>
          <template v-if="section === 'rewards' && gateway && workbuddyReady">
            <a-button :loading="gatewayWorking === 'compensation_all'" :disabled="!!gatewayWorking" @click="gatewayBatch('compensation_all', '全账号补偿领取')"><template #icon><icon-gift /></template>全账号补偿</a-button>
            <a-button :loading="gatewayWorking === 'travel_all'" :disabled="!!gatewayWorking" @click="gatewayBatch('travel_all', '旅行巡检')">旅行巡检</a-button>
            <a-button :loading="gatewayWorking === 'activity_all'" :disabled="!!gatewayWorking" @click="gatewayBatch('activity_all', '活跃上报')">活跃上报</a-button>
            <a-button :loading="gatewayWorking === 'keepalive_all'" :disabled="!!gatewayWorking" @click="gatewayBatch('keepalive_all', 'Token 保活')">Token 保活</a-button>
          </template>
          <a-button v-if="section === 'rewards' && workbuddyReady" type="primary" :disabled="working || loading || queue.running || !claimCount" @click="confirmation = { title: '领取全部奖励', rewards: true }"><template #icon><icon-gift /></template>全部领取</a-button>
        </template>
      </div></div>
    </header>

    <a-alert v-if="error" type="error" class="benefit-notice">{{ error }}</a-alert>
    <a-alert v-if="result" type="info" class="benefit-notice">{{ result }}</a-alert>
    <a-alert v-if="unsupportedAccounts.length" type="warning" class="benefit-notice"><div v-for="item in unsupportedAccounts" :key="item.uid">{{ model.accountName(item) }}：{{ item.benefits_reason || '当前登录身份暂不支持个人任务和活动奖励' }}</div></a-alert>

    <a-spin :loading="loading" class="benefit-content">
      <template v-if="section === 'tasks'">
        <a-empty v-if="!supported" :description="model.providerName + ' 暂未接入成长任务'" />
        <template v-else>
          <div class="stats-band benefit-stats">
            <div class="metric"><span class="metric-label">任务完成度</span><strong>{{ completedTasks }} / {{ tasks.length }}</strong><small>{{ Math.round(taskProgress * 100) }}% · 当前账号</small></div>
            <div class="metric"><span class="metric-label">待执行</span><strong class="warning-text">{{ tasks.filter(t => !t.claimed && !t.locked).length }}</strong><small>{{ tasks.filter(t => t.claimable).length }} 项可领取奖励</small></div>
            <div class="metric"><span class="metric-label">执行中</span><strong>{{ queue.items?.filter(i => i.status === 'running').length || 0 }}</strong><small>{{ queue.running ? '队列运行中' : '队列空闲' }}</small></div>
            <div class="metric"><span class="metric-label">待领取积分</span><strong class="accent-text">+{{ formatNumber(tasks.filter(t => t.claimable).reduce((sum, t) => sum + Number(t.credit || 0), 0)) }}</strong><small>当前账号 · 已达成任务</small></div>
          </div>

          <div class="benefit-layout">
            <section class="benefit-primary">
              <div class="benefit-panel-heading">
                <div class="benefit-panel-title"><h2>成长任务队列</h2><span>共 {{ tasks.length }} 项</span></div>
                <a-radio-group v-model="filter" type="button" size="small" aria-label="任务状态"><a-radio value="all">全部</a-radio><a-radio value="pending">待执行</a-radio><a-radio value="claimable">待领取</a-radio><a-radio value="claimed">已完成</a-radio><a-radio value="error">失败</a-radio></a-radio-group>
              </div>
              <div class="benefit-toolbar">
                <a-select :model-value="uid" :disabled="working" placeholder="选择账号" aria-label="查看账号" @change="selectAccount"><a-option v-for="item in options" :key="item.uid" :value="item.uid">{{ model.accountName(item) }}</a-option></a-select>
                <a-input v-model="search" allow-clear placeholder="搜索任务" aria-label="搜索任务"><template #prefix><icon-search /></template></a-input>
                <a-button :loading="loading" :disabled="working" aria-label="刷新任务" title="刷新任务" @click="refresh"><template #icon><icon-refresh /></template></a-button>
              </div>
              <div class="benefit-overall-progress"><div class="inline-between"><span>总体进度</span><strong>{{ completedTasks }} / {{ tasks.length }} · {{ Math.round(taskProgress * 100) }}%</strong></div><a-progress :percent="taskProgress" :show-text="false" /></div>
              <div class="benefit-task-scroll">
              <a-table class="benefit-task-table" :data="taskRows" row-key="task_code" :pagination="{ pageSize: 8, simple: true }" :bordered="false">
                <template #columns>
                  <a-table-column title="任务" :width="270"><template #cell="{ record }"><div class="task-identity"><span :class="['task-symbol', record.claimed ? 'complete' : record.claimable ? 'available' : '']"><icon-check v-if="record.claimed" /><icon-gift v-else-if="record.claimable" /><icon-thunderbolt v-else /></span><div><strong>{{ record.title || record.task_code }}</strong><small class="benefit-detail">{{ record.task_desc || record.description || record.task_code }}</small><small v-if="record.target" class="benefit-detail">进度 {{ record.current }} / {{ record.target }}</small></div></div></template></a-table-column>
                  <a-table-column title="奖励" :width="95" align="right"><template #cell="{ record }"><strong class="accent-text">+{{ formatNumber(record.credit) }}</strong><small class="benefit-detail">积分<span v-if="record.energy"> · {{ record.energy }} 能量</span></small></template></a-table-column>
                  <a-table-column title="状态" :width="100"><template #cell="{ record }"><a-tag :color="taskState(record).color">{{ taskState(record).label }}</a-tag></template></a-table-column>
                  <a-table-column title="操作" :width="140"><template #cell="{ record }"><a-button v-if="record.claimable" type="text" :disabled="working || queue.running" @click="execute('claim', '领取奖励', { task_code: record.task_code })"><template #icon><icon-gift /></template>领取</a-button><template v-else-if="!record.claimed && !record.locked"><a-button v-if="!record.accept_status || record.accept_status === 'not_accepted'" type="text" :disabled="working || queue.running" @click="execute('accept', '接受任务', { task_codes: [record.task_code] })">接受</a-button><a-button v-if="automatic.includes(record.task_code)" type="text" :disabled="working || queue.running" @click="confirmTask(record)"><template #icon><icon-play-arrow /></template>执行</a-button></template></template></a-table-column>
                </template>
                <template #empty><a-empty :description="account ? '暂无符合条件的任务' : '暂无可用的国内版个人账号'" /></template>
              </a-table>
              </div>
              <div class="benefit-panel-footer"><span><i class="status-dot" />{{ loadedAt ? '最近查询 ' + date(loadedAt).split(' ')[1] : '等待查询' }}</span><span>{{ account ? model.accountName(account) : '未选择账号' }}</span></div>
            </section>

            <aside class="benefit-aside">
              <section class="benefit-side-section">
                <div class="inline-between"><h2>执行队列</h2><a-tag :color="queue.running ? 'arcoblue' : 'gray'">{{ queue.running ? '执行中' : '已结束' }} · {{ done }} / {{ queue.total || 0 }}</a-tag></div>
                <div class="benefit-overall-progress"><div class="inline-between"><span>本轮进度</span><strong>{{ done }} / {{ queue.total || 0 }}</strong></div><a-progress :percent="queue.total ? done / queue.total : 0" :show-text="false" /></div>
                <div v-for="(item, index) in queue.items || []" :key="index" class="queue-line"><div class="inline-between"><strong>{{ item.nickname || item.uid }}</strong><a-tag :color="item.status === 'error' ? 'red' : item.status === 'done' ? 'arcoblue' : 'gray'">{{ statusName(item.status) }}</a-tag></div><small>{{ item.code }}</small><small v-if="item.message">{{ item.message }}</small></div>
                <a-empty v-if="!queue.items?.length" description="暂无执行中的任务" />
                <a-select v-model="concurrency" aria-label="并行账号数" :disabled="queue.running || working"><a-option v-for="n in [1, 2, 3, 4]" :key="n" :value="n">{{ n }} 个账号并行</a-option></a-select>
                <a-button long type="primary" :disabled="working || queue.running || !personal.length" @click="confirmTask(null)"><template #icon><icon-play-arrow /></template>开始执行队列</a-button>
              </section>
              <section class="benefit-side-section">
                <div class="inline-between"><h2>账号覆盖</h2><a href="#accounts">查看账号 <icon-arrow-right /></a></div>
                <div v-for="item in coverage" :key="item.uid" class="coverage-line"><div class="inline-between"><span class="account-caption"><span class="account-initial">{{ model.accountName(item).slice(0, 1) }}</span><span>{{ model.accountName(item) }}</span></span><small>{{ item.uid === uid && tasks.length ? completedTasks + ' / ' + tasks.length : item.disabled ? '已停用' : !personal.some(a => a.uid === item.uid) ? '暂不支持' : item.pendingTasks?.growth_error ? '查询失败' : item.pendingTasks ? (item.pendingTasks.growth || []).length + ' 项待办' : '待扫描' }}</small></div><a-progress v-if="item.uid === uid && tasks.length" :percent="taskProgress" :show-text="false" /></div>
                <div class="benefit-side-note"><icon-info-circle />{{ personal.length }} 个账号可参与个人任务</div>
              </section>
              <section v-if="scan" class="benefit-side-section"><h2>当前应用待办 · {{ scanPending }} 项</h2><div v-for="item in scanAccounts" :key="item.uid" class="scan-line"><strong>{{ item.nickname || item.uid }}</strong><span :class="{ 'danger-text': item.growth_error }">{{ item.growth_error || ((item.growth || []).length + ' 项待办') }}</span></div></section>
            </aside>
          </div>
        </template>
      </template>

      <template v-else>
        <template v-if="model.provider === 'qoder' && model.qoderAccounts.length">
          <div class="reward-feature"><div><div class="eyebrow">DAILY REWARD · QODER</div><h2>每日 100 Credits</h2><p>领取状态以平台当前返回的结果为准</p></div><icon-gift class="reward-feature-icon" /></div>
          <div class="reward-grid"><article v-for="item in model.qoderAccounts" :key="item.uid" class="reward-item"><span class="reward-symbol"><icon-gift /></span><a-tag>{{ item.checkin_done ? '本轮已领取' : '本轮待领取' }}</a-tag><h3>{{ model.accountName(item) }}</h3><p>当前余额 {{ accountCredits(item) }} 积分</p><div class="reward-amount">+100 <small>Credits</small></div><a-button long :disabled="item.disabled || item.checkin_done" :loading="model.busy.has(item.uid + ':checkin')" @click="model.accountAction(item, 'checkin').catch(() => {})">领取奖励</a-button></article></div>
        </template>
        <template v-else-if="workbuddyReady">
          <div class="benefit-toolbar reward-account-select"><a-select :model-value="uid" :disabled="working" placeholder="选择账号" aria-label="查看账号" @change="selectAccount"><a-option v-for="item in options" :key="item.uid" :value="item.uid">{{ model.accountName(item) }}</a-option></a-select><span class="benefit-toolbar-hint">{{ account ? '当前账号 · ' + model.accountName(account) : '请选择国内版个人账号' }}</span></div>
          <a-alert v-for="(message, name) in rewards?.errors || {}" :key="name" type="warning" class="benefit-notice">{{ name }}：{{ message }}</a-alert>
          <div class="reward-feature">
            <div class="reward-feature-copy"><div class="eyebrow">OFFICIAL REWARD · 官方补偿</div><h2>{{ rewards?.compensation?.active ? '服务异常补偿' : '官方补偿' }}</h2><p>{{ !account ? '暂无可用的国内版个人账号' : !rewards?.compensation ? '暂未返回补偿状态' : rewards.compensation.active ? (rewards.compensation.claimed ? '本次补偿已领取' : '当前账号有可领取的官方补偿') : '当前无生效中的补偿活动' }}</p><div class="reward-feature-meta"><span v-if="rewards?.compensation?.end_time"><icon-clock-circle /> 截止 {{ rewards.compensation.end_time }}</span><a-button type="text" @click="jumpTo(rulesView)">查看规则 <icon-arrow-right /></a-button></div></div>
            <div class="reward-feature-value"><strong>{{ canCompensate ? '+' + formatNumber(rewards.compensation.credit_num) : '--' }}</strong><span>待领取积分</span><a-button :disabled="working || queue.running || !canCompensate" @click="execute('reward', '领取补偿', { action: 'compensation' })"><template #icon><icon-gift /></template>领取补偿</a-button></div>
          </div>
          <div class="reward-section-heading"><div><h2>可领取活动</h2><span>以当前账号活动状态为准</span></div><span class="accent-text">待领取 {{ formatNumber(rewardCredits) }} 积分</span></div>
          <div class="reward-grid">
            <article v-for="tier in rewards?.streak?.redemption_status?.tiers || []" :key="tier.tier" class="reward-item"><span class="reward-symbol warm"><icon-calendar /></span><a-tag :color="canRedeem(tier) ? 'orange' : 'gray'">{{ statusName(tierStatus(tier)) }}</a-tag><h3>{{ tier.days }} 天连登奖励</h3><p>连续 {{ rewards?.streak?.streak?.days ?? '--' }} 天 · 补签卡 {{ rewards?.streak?.makeup_cards?.balance ?? '--' }} 张</p><div class="reward-amount">+{{ formatNumber(tier.credit) }} <small>积分</small></div><div class="reward-item-meta">{{ tier.energy || 0 }} 能量 · {{ tier.chances || 0 }} 次抽奖</div><a-button long :disabled="working || queue.running || !canRedeem(tier)" @click="execute('reward', '兑换奖励', { action: 'redeem', tier: tier.tier })">兑换</a-button></article>
            <article class="reward-item"><span class="reward-symbol"><icon-gift /></span><a-tag :color="rewards?.chances > 0 ? 'arcoblue' : 'gray'">{{ rewards?.chances > 0 ? '可参与' : '暂无机会' }}</a-tag><h3>活动抽奖</h3><p>当前账号可用抽奖机会</p><div class="reward-amount">{{ rewards?.chances ?? '--' }} <small>次</small></div><div class="reward-item-meta">每次抽奖消耗 1 次机会</div><a-button long :disabled="working || queue.running || !(rewards?.chances > 0)" @click="execute('reward', '抽奖', { action: 'draw' })">抽奖一次</a-button></article>
          </div>
          <div class="benefit-layout reward-bottom">
            <section class="benefit-primary">
              <div class="benefit-panel-heading"><div class="benefit-panel-title"><h2>活动券码</h2><span>{{ rewards?.vouchers?.length || 0 }} 张</span></div></div>
              <div v-for="voucher in rewards?.vouchers || []" :key="voucher.grant_id" class="voucher-line"><span class="reward-symbol"><icon-gift /></span><div><strong>{{ voucher.prize_name || voucher.sku_code }}</strong><code>{{ voucher.code }}</code><small class="benefit-detail">有效期至 {{ voucher.valid_to || '--' }}</small></div><a-button aria-label="复制券码" title="复制券码" @click="copy(voucher.code)"><template #icon><icon-copy /></template></a-button></div>
              <a-empty v-if="!rewards?.vouchers?.length" description="暂无活动券码" />
            </section>
            <section ref="rulesView" class="benefit-side-section reward-rules"><h2>活动规则</h2><p><icon-info-circle /><span>奖励按当前账号的活动资格发放。</span></p><p><icon-clock-circle /><span>领取后的有效期以平台返回为准。</span></p><p><icon-check /><span>已领取的档位不可重复兑换。</span></p><p v-if="rewards?.compensation?.end_time">补偿截止：{{ rewards.compensation.end_time }}</p></section>
          </div>
          <section class="benefit-side-section benefit-packages">
            <div class="benefit-panel-heading"><div class="benefit-panel-title"><h2>积分构成</h2><span>逐账号实时查询上游套餐包</span></div><a-button size="small" :loading="packagesLoading" @click="loadPackages"><template #icon><icon-refresh /></template>刷新</a-button></div>
            <div class="table-wrap"><table>
              <thead><tr><th>账号</th><th>剩余 / 总额</th><th>套餐包</th><th>最早到期</th><th>状态</th></tr></thead>
              <tbody>
                <template v-for="row in packageRows" :key="row.uid">
                  <tr :class="{ 'package-open': expandedPackage === row.uid }">
                    <td>{{ model.accountName({ uid: row.uid, nickname: row.nickname }) }}</td>
                    <td><strong class="accent-text">{{ formatNumber(row.remain) }}</strong><small v-if="row.size"> / {{ formatNumber(row.size) }}</small></td>
                    <td>{{ row.packages?.length || 0 }} 个 <a-button v-if="row.packages?.length" type="text" size="mini" @click="expandedPackage = expandedPackage === row.uid ? '' : row.uid">{{ expandedPackage === row.uid ? '收起' : '明细' }}</a-button></td>
                    <td>{{ row.earliestExpiry }}</td>
                    <td><span :class="row.error ? 'danger-text' : ''">{{ row.error || '正常' }}</span></td>
                  </tr>
                  <tr v-if="expandedPackage === row.uid && row.packages?.length" class="package-detail-row">
                    <td colspan="5"><div v-for="(pkg, index) in row.packages" :key="index" class="voucher-line"><span class="reward-symbol"><icon-gift /></span><div><strong>{{ pkg.name }}</strong><small class="benefit-detail">剩余 {{ formatNumber(pkg.remain) }} / {{ formatNumber(pkg.size) }} · 已用 {{ formatNumber(pkg.used) }}<template v-if="pkg.end_time"> · 失效 {{ expiryDate(pkg.end_time) }}</template></small></div></div></td>
                  </tr>
                </template>
              </tbody>
            </table><a-empty v-if="!packageRows.length && !packagesLoading" description="暂无账号套餐数据" /></div>
          </section>
        </template>
        <a-empty v-else :description="supported || model.provider === 'qoder' ? '添加 ' + model.providerName + ' 账号后即可领取活动奖励' : model.providerName + ' 暂未接入活动奖励'" />
      </template>
    </a-spin>
    <a-modal :visible="!!confirmation" :title="confirmation?.title" :footer="false" :mask-closable="false" @cancel="confirmation = null">
      <template v-if="confirmation?.rewards"><p>为「{{ account ? model.accountName(account) : '' }}」领取 {{ claimCount }} 项奖励，共 {{ formatNumber(rewardCredits) }} 积分。</p><p class="modal-description">包含当前可兑换的连登奖励和官方补偿。</p></template>
      <template v-else><p>{{ confirmation?.task?.title || '全部国内版个人账号的可执行待办' }}</p><p class="modal-description">任务可能发起真实对话并消耗积分，完成后会尝试领取奖励。</p></template>
      <div class="modal-actions"><a-button @click="confirmation = null">取消</a-button><a-button type="primary" :loading="working" @click="confirmExecution"><template #icon><icon-play-arrow /></template>{{ confirmation?.rewards ? '确认领取' : '确认执行' }}</a-button></div>
    </a-modal>
  </section>
</template>
