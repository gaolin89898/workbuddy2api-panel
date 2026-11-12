<script setup>
import { ref } from 'vue';
import { Alert as AAlert, Button as AButton, Input as AInput, InputPassword as AInputPassword } from '@arco-design/web-vue';
import { IconFile, IconSafe } from '@arco-design/web-vue/es/icon';
const props = defineProps({ model: Object });
const tab = ref('local');
const quiet = promise => promise?.catch(() => {});
</script>

<template>
  <div class="dialog-tabs" role="tablist" aria-label="Loomy 添加方式"><button role="tab" :aria-selected="tab === 'local'" :class="{ active: tab === 'local' }" @click="tab = 'local'"><icon-file />本机导入</button><button role="tab" :aria-selected="tab === 'token'" :class="{ active: tab === 'token' }" @click="tab = 'token'"><icon-safe />粘贴凭证</button></div>
  <a-alert v-if="model.loomyDraft.uid" type="info">正在更新 {{ model.loomyDraft.nickname || model.loomyDraft.account_uid }}，请提交同一账号的会话。</a-alert>
  <div v-if="tab === 'local'" class="login-panel" role="tabpanel">
    <p class="dialog-intro">Loomy · 读取本机已登录桌面端的会话</p>
    <a-button type="primary" long size="large" :loading="model.busy.has('loomy-import')" @click="quiet(model.importLoomyLocal())"><template #icon><icon-file /></template>读取本机登录信息</a-button>
    <p class="dialog-login-status">需要本机已安装并登录 Loomy 桌面端；账号以掩码手机号备注，可稍后改名。</p>
  </div>
  <form v-else class="key-form" @submit.prevent="quiet(model.addLoomy())">
    <label>账号备注<a-input v-model="model.loomyDraft.nickname" :max-length="80" placeholder="选填" :input-attrs="{ 'aria-label': 'Loomy 账号备注' }" /></label>
    <label>用户 ID（userid）<a-input v-model="model.loomyDraft.account_uid" placeholder="auth-session.json 中的 userid" :input-attrs="{ 'aria-label': 'Loomy 用户 ID', autocomplete: 'off' }" /></label>
    <label>会话 Token（session）<a-input-password v-model="model.loomyDraft.credential" placeholder="auth-session.json 中的 session" :input-attrs="{ 'aria-label': 'Loomy 会话 Token', autocomplete: 'off' }" /></label>
    <div class="modal-actions"><a-button type="primary" html-type="submit" :disabled="!model.loomyDraft.credential.trim() || !model.loomyDraft.account_uid.trim()" :loading="model.busy.has('loomy-add')">验证并添加</a-button></div>
  </form>
</template>
