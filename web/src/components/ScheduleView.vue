<script setup>
import { computed, reactive, watch } from 'vue';
import { Button as AButton, Switch as ASwitch, Select as ASelect, Option as AOption, Tag as ATag, Progress as AProgress } from '@arco-design/web-vue';
import { IconCheck, IconClockCircle, IconUser, IconMinusCircle, IconArrowRight, IconCalendar } from '@arco-design/web-vue/es/icon';
import { chinaDate, clockTime, formatHour } from '../useAccounts';

const props = defineProps({ model: Object, embedded: Boolean });
const emit = defineEmits(['navigate', 'key']);
const draft = reactive({ checkin_enabled: true, checkin_hours: [9, 21], refresh_after_checkin: true, catchup_on_start: true });
const hours = Array.from({ length: 24 }, (_, index) => index);
function reset() {
  if (props.model.schedule) Object.assign(draft, { ...props.model.schedule, checkin_hours: [...props.model.schedule.checkin_hours] });
}
watch(() => props.model.schedule, reset, { immediate: true });
function addHour(hour) {
  if (!draft.checkin_hours.includes(hour)) draft.checkin_hours = [...draft.checkin_hours, hour].sort((a, b) => a - b);
}
function save() {
  const { checkin_enabled, checkin_hours, refresh_after_checkin, catchup_on_start } = draft;
  props.model.saveSchedule({ checkin_enabled, checkin_hours: [...checkin_hours], refresh_after_checkin, catchup_on_start }).catch(() => {});
}
const recentRun = computed(() => [...props.model.allLogs].reverse().find(e => /checkin /i.test(e.text)));
const history = computed(() => Array.from({ length: 7 }, (_, index) => {
  const date = chinaDate(props.model.now - (6 - index) * 86400000);
  const dayLogs = props.model.allLogs.filter(e => chinaDate(e.ts) === date && /checkin /i.test(e.text) && /签到成功|今天已签到|幂等/.test(e.text));
  const today = index === 6;
  return { date, today, weekday: today ? '今天' : new Date(date + 'T00:00:00+08:00').toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai', weekday: 'short' }), label: date.slice(5).replace('-', '/'), count: today ? `${props.model.allSigned.length} / ${props.model.allEligible.length}` : dayLogs.length ? `${dayLogs.length} 次` : '--', kind: today && props.model.allPending.length ? 'warning' : dayLogs.length || today && props.model.allSigned.length ? 'success' : 'muted' };
}));
</script>

<template>
  <section class="schedule-view">
    <header class="page-heading"><div><div class="eyebrow">AUTOMATION · 签到设置</div><h1>签到设置</h1><p>WorkBuddy、Trae 与 MiniMax 共用以下时刻，Qoder 单独按 10:00 轮次处理，服务需保持在线。</p></div><div class="heading-actions"><a-button v-if="embedded" @click="emit('key')">管理密钥</a-button><a-button class="desktop-text" :disabled="!model.pending.length" :loading="model.busy.has('checkin-all')" @click="model.checkinAll().catch(() => {})"><template #icon><icon-check /></template>全部签到</a-button></div></header>
    <div class="settings-grid">
      <form class="schedule-form" @submit.prevent="save">
        <div class="form-top inline-between"><div><h2>每日自动签到</h2><p>在设定时刻自动为可签到账号运行。</p></div><a-switch v-model="draft.checkin_enabled" checked-text="开" unchecked-text="关" aria-label="每日自动签到" /></div>
        <section class="form-section"><div class="inline-between"><h3>签到时刻</h3><small>北京时间 · 0–23 点</small></div><div class="hour-tags"><a-tag v-for="hour in draft.checkin_hours" :key="hour" :closable="draft.checkin_hours.length > 1" @close="draft.checkin_hours = draft.checkin_hours.filter(h => h !== hour)"><icon-clock-circle />{{ formatHour(hour) }}</a-tag><a-select :key="draft.checkin_hours.join(',')" placeholder="添加时刻" class="hour-picker" aria-label="添加签到时刻" @change="addHour"><a-option v-for="hour in hours.filter(h => !draft.checkin_hours.includes(h))" :key="hour" :value="hour">{{ formatHour(hour) }}</a-option></a-select></div></section>
        <section class="form-section"><h3>账号范围</h3><div class="scope-row"><span class="scope-icon"><icon-user /></span><div><strong>参与签到</strong><p>全部平台 · {{ model.allEligible.length }} 个可签到账号 · 停用账号自动跳过</p></div></div><div class="scope-row"><span class="scope-icon muted"><icon-minus-circle /></span><div><strong>自动跳过</strong><p>不支持签到的账号，以及 WorkBuddy 国际版与企业版</p></div></div></section>
        <section class="form-section options-section"><h3>其他选项</h3><label class="switch-row"><span><strong>签到后自动刷新积分</strong><small>使用最新余额更新账号列表</small></span><a-switch v-model="draft.refresh_after_checkin" checked-text="开" unchecked-text="关" aria-label="签到后自动刷新积分" /></label><label class="switch-row"><span><strong>启动时补签当天未签到的账号</strong><small>程序重启后自动补齐</small></span><a-switch v-model="draft.catchup_on_start" checked-text="开" unchecked-text="关" aria-label="启动时补签当天未签到的账号" /></label></section>
        <footer class="form-footer"><span><icon-check />{{ model.savedAt ? `上次保存 ${clockTime(model.savedAt).slice(0, 5)}` : '北京时间 · Asia/Shanghai' }}</span><div><a-button type="text" @click="reset">取消</a-button><a-button type="primary" html-type="submit" :loading="model.busy.has('schedule')" :disabled="!model.schedule"><template #icon><icon-check /></template>保存设置</a-button></div></footer>
      </form>
      <aside class="settings-side">
        <section class="next-run"><div class="inline-between"><span>下次签到</span><span class="side-badge">{{ model.schedule?.checkin_enabled ? '已开启' : '已关闭' }}</span></div><div class="next-time">{{ model.allNextRun?.time || '--:--' }} <small>{{ model.allNextRun?.day || '' }}</small></div><p>{{ model.allNextRun?.date || '自动签到已关闭' }}</p><div class="next-countdown"><icon-clock-circle />{{ model.allNextRun ? `剩余 ${model.allNextRun.remaining}` : '可随时手动签到' }}</div><div class="next-slot" v-for="hour in model.schedule?.checkin_hours || []" :key="hour"><icon-calendar />{{ formatHour(hour) }}<span>北京时间</span></div></section>
        <section class="runtime-section"><div class="inline-between"><h2>运行状态</h2><small>实时</small></div><dl><div><dt>上次运行</dt><dd>{{ recentRun ? clockTime(recentRun.ts) : '--' }}</dd></div><div><dt>今日签到</dt><dd class="accent-text">{{ model.allSigned.length }} 已签 · {{ model.allPending.length }} 待签</dd></div><div><dt>运行时区</dt><dd>北京时间</dd></div><div><dt>服务连接</dt><dd :class="model.connected ? 'accent-text' : 'warning-text'">{{ model.connected ? '已连接' : '未连接' }}</dd></div></dl><div class="inline-between progress-caption"><span>今日签到进度</span><strong>{{ model.allSigned.length }} / {{ model.allEligible.length }}</strong></div><a-progress :percent="model.allEligible.length ? Math.min(1, model.allSigned.length / model.allEligible.length) : 0" :show-text="false" color="var(--accent)" size="small" /></section>
      </aside>
    </div>
    <p class="qoder-reward-note">Qoder 每轮奖励在 10:00 更新，早于 10 点的签到时刻将顺延至 10:00，其他平台不变。</p>
    <section class="history-section"><div class="inline-between"><h2>最近 7 天 <small>签到记录</small></h2><a-button type="text" size="mini" @click="emit('navigate', 'logs')">查看运行日志<icon-arrow-right /></a-button></div><div class="history-days"><div v-for="day in history" :key="day.date" :class="['history-day', { today: day.today }]"><span>{{ day.weekday }}</span><strong>{{ day.label }}</strong><small :class="day.kind">{{ day.count }}</small></div></div></section>
  </section>
</template>
