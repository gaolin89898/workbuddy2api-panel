<script setup>
import { Alert as AAlert, Button as AButton, Input as AInput } from '@arco-design/web-vue';
import { IconExport } from '@arco-design/web-vue/es/icon';
defineProps({ model: Object, provider: String, name: String });
const quiet = promise => promise?.catch(() => {});
</script>

<template>
  <div class="login-panel" role="tabpanel">
    <a-alert v-if="model[provider + 'Draft'].uid" type="info">正在重新授权 {{ model[provider + 'Draft'].nickname || name }}，请登录同一账号。</a-alert>
    <p class="dialog-intro">{{ name }} · 完成网页登录后自动保存账号</p>
    <label v-if="provider !== 'doubao'" class="web-login-remark">账号备注<a-input v-model="model[provider + 'Draft'].nickname" :max-length="80" placeholder="选填" :disabled="model.webLogins[provider].waiting" :input-attrs="{ 'aria-label': name + '账号备注' }" /></label>
    <a-alert v-if="model.webLogins[provider].error" type="error" class="login-error">{{ model.webLogins[provider].error }}</a-alert>
    <a-button v-if="!model.webLogins[provider].waiting" type="primary" long size="large" :loading="model.busy.has(provider + '-login')" @click="quiet(model.startWebLogin(provider))"><template #icon><icon-export /></template>{{ model.webLogins[provider].error ? '重新开始网页授权' : '开始网页授权' }}</a-button>
    <div v-else class="authorization">
      <a v-if="model.webLogins[provider].url" class="authorization-link" :href="model.webLogins[provider].url" target="_blank" rel="noopener noreferrer"><icon-export />打开 Google 授权页面</a>
      <div class="inline-between"><span><i class="status-dot" />{{ model.webLogins[provider].status }}</span><a-button type="text" @click="model.stopWebLogin(provider)">取消本次授权</a-button></div>
    </div>
    <p class="dialog-login-status">{{ provider === 'antigravity' ? '使用 Google 账号登录并同意授权，无需填写 OAuth 凭证。' : '在打开的豆包网页中完成登录，无需复制 Cookie 或 Token。' }}</p>
    <p class="dialog-login-status">请在运行面板的电脑上通过 localhost 或 127.0.0.1 使用网页授权。</p>
  </div>
</template>

<style scoped>
.web-login-remark { display: grid; gap: 8px; margin: 0 0 20px; color: var(--secondary); }
</style>
