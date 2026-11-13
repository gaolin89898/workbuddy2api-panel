<script setup>
import { ref } from 'vue';
import { Alert as AAlert, Button as AButton } from '@arco-design/web-vue';
import { IconFile, IconExport, IconUpload } from '@arco-design/web-vue/es/icon';
import WebAccountLogin from './WebAccountLogin.vue';
const props = defineProps({ model: Object });
const tab = ref('login');
const fileInput = ref(null), file = ref(null), result = ref(null);
const quiet = promise => promise?.catch(() => {});
async function importFile() { result.value = await props.model.importAntigravity(file.value); }
function selectTab(value) { props.model.stopWebLogin('antigravity'); tab.value = value; }
</script>

<template>
  <div class="dialog-tabs" role="tablist" aria-label="Antigravity 添加方式"><button role="tab" :aria-selected="tab === 'login'" :class="{ active: tab === 'login' }" @click="selectTab('login')"><icon-export />网页授权</button><button role="tab" :aria-selected="tab === 'import'" :class="{ active: tab === 'import' }" @click="selectTab('import')"><icon-file />导入授权 JSON</button></div>
  <WebAccountLogin v-if="tab === 'login'" :model="model" provider="antigravity" name="Antigravity" />
  <div v-else class="import-panel" role="tabpanel">
    <input ref="fileInput" type="file" accept=".json,application/json" class="file-input" aria-label="选择 Antigravity 授权 JSON" @change="file = $event.target.files?.[0] || null; result = null">
    <button class="file-drop" @click="fileInput.click()"><icon-upload /><strong>{{ file?.name || '选择授权 JSON' }}</strong><small>支持单账号或账号列表 · 最大 4 MB</small></button>
    <a-alert v-if="result" :type="result.errors?.length ? 'warning' : 'success'">已导入 {{ result.imported }} 个账号<div v-for="error in result.errors" :key="error">{{ error }}</div></a-alert>
    <a-button type="primary" long size="large" :disabled="!file" :loading="model.busy.has('antigravity-import')" @click="quiet(importFile())">验证并导入</a-button>
  </div>
</template>
