<script setup>
import { ref } from 'vue';
import { Alert as AAlert, Button as AButton, Input as AInput, InputPassword as AInputPassword, Select as ASelect, Option as AOption } from '@arco-design/web-vue';
import { IconExport, IconSafe } from '@arco-design/web-vue/es/icon';
const props = defineProps({ model: Object });
const tab = ref('login');
const quiet = promise => promise?.catch(() => {});
function selectTab(value) { props.model.stopMiniMaxLogin(); tab.value = value; }
</script>

<template>
  <div class="dialog-tabs" role="tablist" aria-label="MiniMax 添加方式"><button role="tab" :aria-selected="tab === 'login'" :class="{ active: tab === 'login' }" @click="selectTab('login')"><icon-export />浏览器授权</button><button role="tab" :aria-selected="tab === 'token'" :class="{ active: tab === 'token' }" @click="selectTab('token')"><icon-safe />粘贴凭证</button></div>
  <a-alert v-if="model.minimaxDraft.uid" type="info">正在更新 {{ model.minimaxDraft.nickname || model.minimaxDraft.uid }}，请使用同一账号授权。</a-alert>
  <div v-if="tab === 'login'" class="login-panel" role="tabpanel">
    <p class="dialog-intro">MiniMax 国内版 · 设备授权登录（与桌面端一致）</p>
    <a-alert v-if="model.minimaxLogin.error" type="error" class="login-error">{{ model.minimaxLogin.error }}</a-alert>
    <a-button v-if="!model.minimaxLogin.url || model.minimaxLogin.error" type="primary" long size="large" :loading="model.busy.has('minimax-login')" @click="quiet(model.startMiniMaxLogin())"><template #icon><icon-export /></template>{{ model.minimaxLogin.error ? '重新获取授权链接' : '获取授权链接' }}</a-button>
    <div v-if="model.minimaxLogin.url" class="authorization"><a class="authorization-link" :href="model.minimaxLogin.url" target="_blank" rel="noopener noreferrer"><icon-export />打开 MiniMax 授权页面</a><div class="inline-between"><span><i class="status-dot" />{{ model.minimaxLogin.waiting ? '等待浏览器授权' : '授权已暂停' }}</span><a-button type="text" @click="model.stopMiniMaxLogin()">取消本次授权</a-button></div></div>
    <p class="dialog-login-status">完成登录并点击授权后，账号自动保存 · 授权码 {{ model.minimaxLogin.userCode || '--' }}</p>
  </div>
  <form v-else class="key-form" @submit.prevent="quiet(model.addMiniMax())">
    <label>账号备注<a-input v-model="model.minimaxDraft.nickname" :max-length="80" placeholder="选填" :input-attrs="{ 'aria-label': 'MiniMax 账号备注' }" /></label>
    <label>凭证类型<a-select v-model="model.minimaxDraft.auth_kind" aria-label="MiniMax 凭证类型"><a-option value="">自动识别</a-option><a-option value="cookie">Cookie</a-option><a-option value="bearer">Bearer 令牌</a-option></a-select></label>
    <label>Cookie / 令牌<a-input-password v-model="model.minimaxDraft.credential" placeholder="例如 sessionid=…; … 或 Bearer 令牌" :input-attrs="{ 'aria-label': 'MiniMax 凭证', autocomplete: 'off' }" /></label>
    <div class="modal-actions"><a-button type="primary" html-type="submit" :disabled="!model.minimaxDraft.credential.trim()" :loading="model.busy.has('minimax-add')">验证并添加</a-button></div>
  </form>
</template>
