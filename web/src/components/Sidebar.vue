<script setup>
import { computed } from 'vue';
import { Button as AButton, Tooltip as ATooltip } from '@arco-design/web-vue';
import { IconUserGroup, IconCalendar, IconFile, IconSafe, IconMoon, IconSun, IconClockCircle, IconCheckSquare, IconGift, IconThunderbolt, IconBook, IconCalendarClock, IconCopy } from '@arco-design/web-vue/es/icon';
import { formatHour, KEY_STORAGE } from '../useAccounts';

const props = defineProps({ model: Object, view: String, dark: Boolean, hostTheme: Boolean });
const emit = defineEmits(['navigate', 'key', 'theme']);
// 网关入口仅在 -gateway 能力开启时渲染（capabilities 由后端下发）。
const tabs = computed(() => [...[
  { id: 'accounts', name: '账号', icon: IconUserGroup },
  { id: 'tasks', name: '任务中心', icon: IconCheckSquare },
  { id: 'billing', name: '积分明细', icon: IconBook },
  { id: 'rewards', name: '活动奖励', icon: IconGift },
  { id: 'settings', name: '签到设置', icon: IconCalendar },
  { id: 'schedules', name: '定时任务', icon: IconCalendarClock },
  { id: 'logs', name: '运行日志', icon: IconFile },
], ...(props.model.capabilities?.gateway ? [{ id: 'gateway', name: '网关', icon: IconThunderbolt }] : [])]);
// API Access 卡（设计稿 Sidebar 扩展，仅网关页显示）：端点与密钥复制。
const apiKey = computed(() => props.model.capabilities?.gateway ? (localStorage.getItem(KEY_STORAGE) || '') : '');
function copyKey() {
  navigator.clipboard.writeText(apiKey.value).then(() => {}).catch(() => {});
}
</script>

<template>
  <aside class="sidebar">
    <div class="sidebar-top">
      <a class="brand" href="#accounts" @click.prevent="emit('navigate', 'accounts')">
        <span class="brand-mark">A</span><span><strong>AI 工具箱</strong></span>
      </a>
      <nav class="side-navigation" aria-label="主导航">
        <button v-for="tab in tabs" :key="tab.id" :class="{ active: view === tab.id }" :aria-current="view === tab.id ? 'page' : undefined" @click="emit('navigate', tab.id)"><component :is="tab.icon" /><span>{{ tab.name }}</span></button>
      </nav>
      <section v-if="view === 'gateway'" class="api-access">
        <span class="nav-caption">API ACCESS</span>
        <code class="api-endpoint">POST /v1/chat/completions</code>
        <div class="inline-between api-key-row"><small>Bearer 管理密钥</small><button class="api-copy" :disabled="!apiKey" aria-label="复制密钥" title="复制密钥" @click="copyKey"><icon-copy /></button></div>
        <small class="side-muted">与面板共用 api_key · 鉴权失败返回 401</small>
      </section>
      <div v-if="!['antigravity', 'doubao'].includes(model.provider)" class="sidebar-schedule">
        <div class="inline-between"><span>自动签到</span><span class="side-badge">{{ model.schedule?.checkin_enabled ? '已开启' : '已关闭' }}</span></div>
        <div class="side-times"><template v-for="(hour, index) in model.effectiveHours" :key="hour"><span v-if="index" class="time-separator">·</span><b>{{ formatHour(hour) }}</b></template><span v-if="!model.schedule">--:--</span></div>
        <span class="side-muted">每天 · 北京时间</span>
        <div class="side-next"><icon-clock-circle /><span>{{ model.nextRun ? `下次运行 ${model.nextRun.day} ${model.nextRun.time}` : '自动签到已关闭' }}</span></div>
      </div>
    </div>
    <div class="sidebar-footer">
      <div class="inline-between connection"><span><i :class="['status-dot', { offline: !model.connected }]" />{{ model.connected ? '服务已连接' : '未连接' }}</span><small>127.0.0.1</small></div>
      <div class="side-version">{{ model.overview.version || 'WorkBuddy' }} · Asia/Shanghai</div>
      <div class="footer-buttons"><a-button long class="key-button" @click="emit('key')"><template #icon><icon-safe /></template>管理密钥</a-button><a-tooltip v-if="!hostTheme" :content="dark ? '切换浅色主题' : '切换深色主题'"><a-button class="theme-button" :aria-label="dark ? '切换浅色主题' : '切换深色主题'" @click="emit('theme')"><template #icon><icon-sun v-if="dark" /><icon-moon v-else /></template></a-button></a-tooltip></div>
    </div>
  </aside>
</template>
