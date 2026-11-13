<script setup>
import { ref } from 'vue';
import { Alert as AAlert, Button as AButton, Input as AInput, InputPassword as AInputPassword } from '@arco-design/web-vue';
import { IconFile, IconSafe, IconUpload } from '@arco-design/web-vue/es/icon';
const props = defineProps({ model: Object });
const tab = ref(props.model.antigravityDraft.uid ? 'token' : 'import');
const fileInput = ref(null), file = ref(null), result = ref(null);
const quiet = promise => promise?.catch(() => {});
async function importFile() { result.value = await props.model.importAntigravity(file.value); }
</script>

<template>
  <div class="dialog-tabs" role="tablist" aria-label="Antigravity 添加方式"><button role="tab" :aria-selected="tab === 'import'" :class="{ active: tab === 'import' }" @click="tab = 'import'"><icon-file />导入授权 JSON</button><button role="tab" :aria-selected="tab === 'token'" :class="{ active: tab === 'token' }" @click="tab = 'token'"><icon-safe />OAuth 凭证</button></div>
  <div v-if="tab === 'import'" class="import-panel" role="tabpanel">
    <input ref="fileInput" type="file" accept=".json,application/json" class="file-input" aria-label="选择 Antigravity 授权 JSON" @change="file = $event.target.files?.[0] || null; result = null">
    <button class="file-drop" @click="fileInput.click()"><icon-upload /><strong>{{ file?.name || '选择授权 JSON' }}</strong><small>支持单账号或账号列表 · 最大 4 MB</small></button>
    <a-alert v-if="result" :type="result.errors?.length ? 'warning' : 'success'">已导入 {{ result.imported }} 个账号<div v-for="error in result.errors" :key="error">{{ error }}</div></a-alert>
    <a-button type="primary" long size="large" :disabled="!file" :loading="model.busy.has('antigravity-import')" @click="quiet(importFile())">验证并导入</a-button>
  </div>
  <form v-else class="key-form" @submit.prevent="quiet(model.addAntigravity())">
    <label>账号备注<a-input v-model="model.antigravityDraft.nickname" :max-length="80" placeholder="选填" /></label>
    <label>Google 邮箱<a-input v-model="model.antigravityDraft.email" placeholder="选填，验证后自动读取" autocomplete="off" /></label>
    <label>Refresh Token<a-input-password v-model="model.antigravityDraft.refresh_token" placeholder="Antigravity OAuth 刷新令牌" autocomplete="off" /></label>
    <label>Access Token<a-input-password v-model="model.antigravityDraft.access_token" placeholder="选填，仅有此令牌时不能自动续期" autocomplete="off" /></label>
    <label>Project ID<a-input v-model="model.antigravityDraft.project_id" placeholder="选填，默认从账号读取" autocomplete="off" /></label>
    <div class="modal-actions"><a-button type="primary" html-type="submit" :disabled="!model.antigravityDraft.refresh_token.trim() && !model.antigravityDraft.access_token.trim()" :loading="model.busy.has('antigravity-add')">验证并添加</a-button></div>
  </form>
</template>
