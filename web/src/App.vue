<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { Alert as AAlert, Button as AButton, Input as AInput, Message, Modal as AModal, Scrollbar as AScrollbar, Tooltip as ATooltip } from '@arco-design/web-vue';
import { IconUserGroup, IconCalendar, IconFile, IconSafe, IconMoon, IconSun, IconPlus, IconCheckSquare, IconGift, IconThunderbolt, IconBook, IconCalendarClock } from '@arco-design/web-vue/es/icon';
import { useAccounts } from './useAccounts';
import { followHostTheme } from './hostTheme';
import { hostRuntime } from './hostRuntime';
import Sidebar from './components/Sidebar.vue';
import AccountView from './components/AccountView.vue';
import ScheduleView from './components/ScheduleView.vue';
import SchedulesView from './components/SchedulesView.vue';
import LogsView from './components/LogsView.vue';
import BenefitsView from './components/BenefitsView.vue';
import BillingView from './components/BillingView.vue';
import GatewayView from './components/GatewayView.vue';
import AccountDialog from './components/AccountDialog.vue';
import PlatformSelect from './components/PlatformSelect.vue';

const localStorage = hostRuntime.storage;
const model = reactive(useAccounts());
const views = ['accounts', 'tasks', 'billing', 'rewards', 'settings', 'schedules', 'logs', 'gateway'];
const readView = () => views.includes(location.hash.slice(1)) ? location.hash.slice(1) : 'accounts';
const view = ref(readView()), dark = ref(localStorage.getItem('wb.accounts.theme') === 'dark');
const keyVisible = ref(false), keyLoading = ref(false), keyError = ref('');
const remarkAccount = ref(null), remark = ref(''), removeAccount = ref(null);
const switchAccount = ref(null);
const workspaceScrollbar = ref(null);
const hostTheme = ref(false);
const embedded = hostRuntime.embedded;
// 网关视图依赖 -gateway 能力：能力关闭时回落账号页（菜单也不渲染入口）。
watch(() => model.capabilities, caps => {
  if (view.value === 'gateway' && caps && !caps.gateway) navigate('accounts');
}, { flush: 'sync' });
function requestSwitch(account) {
  if (model.desktopBusy || model.switchReason(account)) return;
  switchAccount.value = account;
}
async function switchClient() {
  if (!switchAccount.value || model.desktopBusy) return;
  try { await model.switchDesktop(switchAccount.value); switchAccount.value = null; } catch { /* Preserve the confirmation for retry. */ }
}
const tabs = computed(() => [...[{ id: 'accounts', name: '账号', icon: IconUserGroup }, { id: 'tasks', name: '任务', icon: IconCheckSquare }, { id: 'billing', name: '明细', icon: IconBook }, { id: 'rewards', name: '奖励', icon: IconGift }, { id: 'settings', name: '签到', icon: IconCalendar }, { id: 'schedules', name: '定时', icon: IconCalendarClock }, { id: 'logs', name: '日志', icon: IconFile }], ...(model.capabilities?.gateway ? [{ id: 'gateway', name: '网关', icon: IconThunderbolt }] : [])]);
function navigate(value) { view.value = value; location.hash = value; }
watch(view, () => workspaceScrollbar.value?.scrollTo({ top: 0 }), { flush: 'post' });
function hashChanged() { view.value = readView(); }
window.addEventListener('hashchange', hashChanged);
onBeforeUnmount(() => window.removeEventListener('hashchange', hashChanged));
watch(dark, value => {
  document.body.setAttribute('arco-theme', value ? 'dark' : 'light');
  document.documentElement.dataset.theme = value ? 'dark' : 'light';
  if (!hostTheme.value) localStorage.setItem('wb.accounts.theme', value ? 'dark' : 'light');
}, { immediate: true, flush: 'sync' });
onBeforeUnmount(followHostTheme(value => { hostTheme.value = true; dark.value = value; }));
watch(view, value => { document.title = `${{ accounts: '账号管理', tasks: '任务中心', billing: '积分明细', rewards: '活动奖励', settings: '签到设置', schedules: '定时任务', logs: '运行日志', gateway: '网关' }[value]} · AI 工具箱`; }, { immediate: true });
async function openKey() {
  keyVisible.value = true;
  keyLoading.value = true;
  keyError.value = '';
  try { await model.refreshKey(); } catch (cause) { keyError.value = cause.message; }
  finally { keyLoading.value = false; }
}
async function copyKey() {
  try { await navigator.clipboard.writeText(model.managementKey); Message.success('管理密钥已复制'); }
  catch { Message.error('复制失败，请选中密钥手动复制'); }
}
function editRemark(account) { remarkAccount.value = account; remark.value = model.remarks[account.uid] || ''; }
function saveRemark() { model.saveRemark(remarkAccount.value, remark.value); remarkAccount.value = null; }
async function remove() {
  try { await model.accountAction(removeAccount.value, 'remove'); removeAccount.value = null; } catch { /* Keep the confirmation open for retry. */ }
}
</script>

<template>
  <div :class="['app-shell', { 'dsh-embedded': embedded }]">
    <Sidebar :model="model" :view="view" :dark="dark" :host-theme="hostTheme" @navigate="navigate" @key="openKey" @theme="dark = !dark" />
    <a-scrollbar ref="workspaceScrollbar" outer-class="workspace" class="workspace-scroll-content" tabindex="0" aria-label="页面内容">
      <header class="mobile-header"><a class="brand" href="#accounts" @click.prevent="navigate('accounts')"><span class="brand-mark">A</span><span><strong>AI 工具箱</strong></span></a><div><a-tooltip v-if="!hostTheme" :content="dark ? '浅色主题' : '深色主题'"><a-button :aria-label="dark ? '切换浅色主题' : '切换深色主题'" @click="dark = !dark"><template #icon><icon-sun v-if="dark" /><icon-moon v-else /></template></a-button></a-tooltip><a-tooltip v-if="model.provider !== 'loomy'" content="添加账号"><a-button type="primary" aria-label="添加账号" @click="model.openAdd()"><template #icon><icon-plus /></template></a-button></a-tooltip></div></header>
      <main :class="{ 'billing-workspace': view === 'billing', 'tasks-workspace': view === 'tasks' }">
        <a-alert v-if="model.error" type="warning" class="connection-alert">{{ model.error }}<template #action><a-button size="mini" @click="model.connect().catch(() => {})">重新连接</a-button></template></a-alert>
        <AccountView v-if="view === 'accounts'" :key="model.platformSelection" :model="model" @remark="editRemark" @remove="removeAccount = $event" @switch="requestSwitch" @navigate="navigate"><template #platform-switcher><PlatformSelect :model="model" /></template></AccountView>
        <ScheduleView v-else-if="view === 'settings'" :model="model" :embedded="embedded" @navigate="navigate" @key="openKey" />
        <SchedulesView v-else-if="view === 'schedules'" :model="model" :embedded="embedded" @navigate="navigate" @key="openKey" />
        <BillingView v-else-if="view === 'billing'" :model="model"><template #platform-switcher><PlatformSelect :model="model" /></template></BillingView>
        <GatewayView v-else-if="view === 'gateway' && model.capabilities?.gateway" :model="model" />
        <BenefitsView v-else-if="['tasks', 'rewards'].includes(view)" :model="model" :section="view"><template #platform-switcher><PlatformSelect :model="model" /></template></BenefitsView>
        <LogsView v-else :model="model" />
      </main>
    </a-scrollbar>
    <nav class="mobile-navigation" aria-label="底部导航"><button v-for="tab in tabs" :key="tab.id" :class="{ active: view === tab.id }" :aria-current="view === tab.id ? 'page' : undefined" @click="navigate(tab.id)"><component :is="tab.icon" /><span>{{ tab.name }}</span></button><button @click="openKey"><icon-safe /><span>密钥</span></button></nav>
    <AccountDialog :model="model" />
    <a-modal :visible="!!switchAccount" title="切换客户端账号" width="440px" :footer="false" :mask-closable="false" @cancel="switchAccount = null"><p class="modal-description">将退出并重新启动 {{ switchAccount?.provider === 'qoder' ? 'Qoder 国内版' : switchAccount?.provider === 'trae' ? 'Trae 国内版' : switchAccount?.realm === 'global' ? 'WorkBuddy AI' : 'WorkBuddy' }}，登录「{{ switchAccount ? model.accountName(switchAccount) : '' }}」。</p><p class="remove-notice">请先保存正在编辑的内容。客户端未正常退出时，将结束它的后台进程。原登录态会自动备份。</p><div class="modal-actions"><a-button :disabled="model.busy.has('desktop-switch')" @click="switchAccount = null">取消</a-button><a-button type="primary" :loading="model.busy.has('desktop-switch')" :disabled="model.desktopBusy" @click="switchClient">已保存工作，开始切换</a-button></div></a-modal>
    <a-modal v-model:visible="keyVisible" title="管理密钥" width="440px" :footer="false"><div class="key-form"><p>用于 API 调用的管理密钥</p><a-input :model-value="keyLoading ? '' : model.managementKey" readonly :placeholder="keyLoading ? '正在读取…' : '未设置管理密钥'" :input-attrs="{ 'aria-label': '管理密钥', autocomplete: 'off' }" /><p v-if="keyError" class="danger-text" role="alert">{{ keyError }}</p><div class="modal-actions"><a-button @click="keyVisible = false">关闭</a-button><a-button type="primary" :disabled="keyLoading || !!keyError || !model.managementKey" @click="copyKey">复制密钥</a-button></div></div></a-modal>
    <a-modal :visible="!!remarkAccount" title="账号备注" width="440px" :footer="false" @cancel="remarkAccount = null"><form @submit.prevent="saveRemark"><p class="modal-description">{{ remarkAccount?.nickname || remarkAccount?.uid }}</p><a-input v-model="remark" :max-length="80" show-word-limit allow-clear placeholder="填写备注" :input-attrs="{ 'aria-label': '账号备注' }" /><div class="modal-actions"><a-button @click="remarkAccount = null">取消</a-button><a-button type="primary" html-type="submit">保存备注</a-button></div></form></a-modal>
    <a-modal :visible="!!removeAccount" title="移除账号" width="440px" :footer="false" @cancel="removeAccount = null"><p class="remove-notice">确定移除「{{ removeAccount ? model.accountName(removeAccount) : '' }}」？账号和本地凭证文件将被删除。</p><div class="modal-actions"><a-button @click="removeAccount = null">取消</a-button><a-button type="primary" status="danger" :loading="removeAccount && model.busy.has(removeAccount.uid + ':remove')" @click="remove">确认移除</a-button></div></a-modal>
  </div>
</template>
