<script setup>
import { computed, nextTick, ref, watch } from 'vue';
import { Button as AButton, Empty as AEmpty, Switch as ASwitch } from '@arco-design/web-vue';
import { IconRefresh, IconClockCircle } from '@arco-design/web-vue/es/icon';
import { clockTime } from '../useAccounts';

const props = defineProps({ model: Object });
const channel = ref('all'), consoleBody = ref(null), follow = ref(true), refreshing = ref(false);
const filters = computed(() => [
  { id: 'all', text: '全部', count: props.model.allLogs.length },
  { id: 'task', text: '签到与账号', count: props.model.allLogs.filter(e => e.ch === 'task').length },
  { id: 'chat', text: '对话', count: props.model.allLogs.filter(e => e.ch === 'chat').length },
  { id: 'sys', text: '系统', count: props.model.allLogs.filter(e => e.ch === 'sys').length },
]);
const entries = computed(() => props.model.allLogs.filter(e => channel.value === 'all' || e.ch === channel.value));
const summary = computed(() => {
  const logs = props.model.allTodayLogs;
  return { total: logs.length, success: logs.filter(e => /签到成功|今天已签到/.test(e.text)).length, skipped: logs.filter(e => /跳过|skip/i.test(e.text)).length, errors: logs.filter(e => /失败|超时|error|warn|failed|timeout/i.test(e.text)).length };
});
function category(entry) {
  if (/失败|超时|error|warn|failed|timeout/i.test(entry.text)) return { name: '警告', kind: 'warning' };
  if (entry.ch === 'task') return /checkin/i.test(entry.text) ? { name: '签到', kind: 'success' } : { name: '账号', kind: 'account' };
  return { name: '系统', kind: 'muted' };
}
async function refresh() {
  refreshing.value = true;
  try { await props.model.refreshLogs(); } catch { /* The connection banner owns API errors. */ }
  finally { refreshing.value = false; }
}
watch(() => [props.model.allLogs, channel.value, follow.value], async () => {
  if (!follow.value) return;
  await nextTick();
  if (consoleBody.value) consoleBody.value.scrollTop = consoleBody.value.scrollHeight;
}, { immediate: true });

</script>

<template>
  <section class="logs-view">
    <header class="page-heading"><div><div class="eyebrow">ALL PLATFORMS · LOGS</div><h1>运行日志</h1><p>全部平台签到与账号任务 · 服务共保留最近 500 条</p></div><div class="log-heading-actions"><div class="filter-tabs" role="group" aria-label="日志频道"><button v-for="item in filters" :key="item.id" :class="{ active: channel === item.id }" :aria-pressed="channel === item.id" @click="channel = item.id">{{ item.text }} <span>{{ item.count }}</span></button></div><a-button :loading="refreshing" @click="refresh"><template #icon><icon-refresh /></template>刷新</a-button></div></header>
    <section class="log-console" aria-label="运行日志控制台"><header class="console-header"><span class="console-dots"><i /><i /><i /></span><span>task.log</span><div class="console-header-right"><span class="side-badge">实时</span><span>自动刷新 5 秒</span></div></header><div ref="consoleBody" class="console-body" role="log" aria-label="运行日志"><div v-for="(entry, index) in entries" :key="entry.ts + ':' + index" class="log-row"><time>{{ clockTime(entry.ts) }}</time><span :class="['log-category', category(entry).kind]">{{ category(entry).name }}</span><span class="log-message">{{ entry.text }}</span></div><a-empty v-if="!entries.length" description="暂无日志" /></div><footer class="console-footer"><span><i :class="['status-dot', { offline: !model.connected }]" />{{ model.connected ? '已连接' : '未连接' }} · 最后更新 {{ model.updatedAt ? clockTime(model.updatedAt) : '--' }}</span><label><span>跟随最新日志</span><a-switch v-model="follow" size="small" aria-label="跟随最新日志" /></label><span>共 {{ entries.length }} 条</span></footer></section>
    <section class="log-summary"><div class="inline-between"><h2>今日运行 <small>从 00:00 起 · 北京时间</small></h2></div><div class="summary-metrics"><div><span>事件总数</span><strong>{{ summary.total }}</strong></div><div><span>签到成功</span><strong class="accent-text">{{ summary.success }}</strong></div><div><span>已跳过</span><strong>{{ summary.skipped }}</strong></div><div><span>异常</span><strong class="warning-text">{{ summary.errors }}</strong></div></div><footer class="inline-between"><span><icon-clock-circle />{{ model.allNextRun ? `下次自动签到 ${model.allNextRun.day} ${model.allNextRun.time}` : '自动签到已关闭' }}</span><span :class="model.connected ? 'accent-text' : 'warning-text'">{{ model.connected ? '服务运行中' : '服务未连接' }}</span></footer></section>
  </section>
</template>
