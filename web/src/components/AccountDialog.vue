<script setup>
import { ref, watch } from 'vue';
import { Alert as AAlert, Button as AButton, Modal as AModal, Message } from '@arco-design/web-vue';
import { IconCheck, IconExport, IconFile, IconInfoCircle, IconSafe, IconUpload, IconCopy } from '@arco-design/web-vue/es/icon';
import TraeAccountForm from './TraeAccountForm.vue';
import QoderAccountForm from './QoderAccountForm.vue';
import MiniMaxAccountForm from './MiniMaxAccountForm.vue';
import LoomyAccountForm from './LoomyAccountForm.vue';

const props = defineProps({ model: Object });
const fileInput = ref(null);
const quiet = promise => promise?.catch(() => {});
watch(() => props.model.realm, () => {
  // Changing region invalidates an authorization link for the previous region.
  if (props.model.addVisible) props.model.resetLogin();
});
watch(() => props.model.addProvider, () => props.model.resetLogin());
function selectFile(event) { props.model.importFile = event.target.files?.[0] || null; props.model.importResult = null; }
async function copyLink() {
  try { await navigator.clipboard.writeText(props.model.login.url); Message.success('授权链接已复制'); }
  catch { Message.error('无法访问剪贴板，请直接打开授权链接'); }
}
</script>

<template>
  <a-modal :visible="model.addVisible" title="添加账号" width="560px" :footer="false" modal-class="account-dialog" :unmount-on-close="true" @cancel="model.closeAdd()">
    <div class="add-platforms" role="group" aria-label="添加账号平台"><button v-for="item in [{ id: 'workbuddy', name: 'WorkBuddy' }, { id: 'trae', name: 'Trae' }, { id: 'qoder', name: 'Qoder' }, { id: 'minimax', name: 'MiniMax' }, { id: 'loomy', name: 'Loomy' }]" :key="item.id" :class="{ active: model.addProvider === item.id }" :aria-pressed="model.addProvider === item.id" @click="model.addProvider = item.id">{{ item.name }}</button></div>
    <TraeAccountForm v-if="model.addProvider === 'trae'" :model="model" />
    <QoderAccountForm v-else-if="model.addProvider === 'qoder'" :model="model" />
    <MiniMaxAccountForm v-else-if="model.addProvider === 'minimax'" :model="model" />
    <LoomyAccountForm v-else-if="model.addProvider === 'loomy'" :model="model" />
    <template v-else>
    <p class="dialog-intro">通过浏览器授权或导入 Cockpit JSON 添加账号。</p>
    <div class="dialog-tabs" role="tablist" aria-label="添加方式"><button role="tab" :aria-selected="model.addTab === 'login'" :class="{ active: model.addTab === 'login' }" @click="model.addTab = 'login'"><icon-export />浏览器登录</button><button role="tab" :aria-selected="model.addTab === 'import'" :class="{ active: model.addTab === 'import' }" @click="model.addTab = 'import'"><icon-file />导入 JSON</button></div>
    <div v-if="model.addTab === 'login'" class="login-panel" role="tabpanel">
      <h3>账号地区</h3><div class="realm-options" role="radiogroup" aria-label="账号地区"><button v-for="region in [{ id: 'cn', name: '国内版', note: '支持每日签到' }, { id: 'global', name: '国际版', note: '仅额度查询' }]" :key="region.id" role="radio" :aria-checked="model.realm === region.id" :class="{ active: model.realm === region.id }" @click="model.realm = region.id"><span><strong>{{ region.name }}</strong><small>{{ region.note }}</small></span><span class="radio-mark"><icon-check v-if="model.realm === region.id" /></span></button></div>
      <div class="region-notice"><icon-info-circle /><span>国际版与企业版账号可查询额度，但不支持个人每日签到。</span></div>
      <a-alert v-if="model.login.error" type="error" class="login-error">{{ model.login.error }}</a-alert>
      <a-button v-if="!model.login.url" type="primary" long size="large" :loading="model.busy.has('login')" @click="quiet(model.startLogin())"><template #icon><icon-export /></template>开始登录</a-button>
      <div v-else class="authorization"><a class="authorization-link" :href="model.login.url" target="_blank" rel="noopener noreferrer"><icon-export />打开授权页面</a><div class="inline-between"><span><i class="status-dot" />{{ model.login.status }}</span><a-button type="text" size="mini" @click="copyLink"><template #icon><icon-copy /></template>复制链接</a-button></div><a-button v-if="model.login.error" long @click="quiet(model.startLogin())">重新获取授权链接</a-button></div>
      <div class="dialog-login-status">{{ model.login.url ? '完成浏览器授权后，账号将自动添加。' : '腾讯账号授权' }}</div>
      <div class="dialog-import-link inline-between"><span>已有 Cockpit 导出文件？</span><a-button type="text" @click="model.addTab = 'import'">导入 JSON<icon-export /></a-button></div>
    </div>
    <div v-else class="import-panel" role="tabpanel"><h3>Cockpit 导出文件</h3><input ref="fileInput" type="file" accept=".json,application/json" aria-label="选择 Cockpit JSON 文件" class="file-input" @change="selectFile"><button class="file-drop" @click="fileInput.click()"><icon-upload /><strong>{{ model.importFile?.name || '选择 JSON 文件' }}</strong><small>{{ model.importFile ? `${(model.importFile.size / 1024).toFixed(1)} KB` : 'Cockpit JSON · 最大 32 MB' }}</small></button><a-alert v-if="model.importResult" :type="model.importResult.errors?.length ? 'warning' : 'success'">已导入 {{ model.importResult.imported || 0 }} 个账号<template v-if="model.importResult.errors?.length"><div v-for="(error, index) in model.importResult.errors" :key="index">{{ typeof error === 'string' ? error : JSON.stringify(error) }}</div></template></a-alert><a-button type="primary" long size="large" :disabled="!model.importFile" :loading="model.busy.has('import')" @click="quiet(model.importAccounts())"><template #icon><icon-upload /></template>导入账号</a-button></div>
    </template>
    <footer class="dialog-footer inline-between"><span><icon-safe />凭证仅保存在本地</span><a-button type="text" @click="model.closeAdd()">取消</a-button></footer>
  </a-modal>
</template>
