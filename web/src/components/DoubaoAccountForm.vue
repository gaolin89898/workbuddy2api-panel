<script setup>
import { Alert as AAlert, Button as AButton, Input as AInput, InputPassword as AInputPassword } from '@arco-design/web-vue';
defineProps({ model: Object });
const quiet = promise => promise?.catch(() => {});
</script>

<template>
  <a-alert v-if="model.doubaoDraft.uid" type="info">更新 {{ model.doubaoDraft.nickname || '豆包账号' }} 的登录凭证。</a-alert>
  <form class="key-form" @submit.prevent="quiet(model.addDoubao())">
    <label>账号备注<a-input v-model="model.doubaoDraft.nickname" :max-length="80" placeholder="选填" :input-attrs="{ 'aria-label': '豆包账号备注' }" /></label>
    <label>sessionid 或 Cookie<a-input-password v-model="model.doubaoDraft.cookie" placeholder="粘贴豆包登录凭证" :input-attrs="{ 'aria-label': '豆包登录凭证', autocomplete: 'off' }" /></label>
    <p class="dialog-login-status">登录 <a href="https://www.doubao.com/chat/" target="_blank" rel="noopener noreferrer">豆包网页版</a>，在开发者工具的 Application → Cookies 中复制 sessionid，或复制网络请求的完整 Cookie。</p>
    <div class="modal-actions"><a-button type="primary" html-type="submit" :disabled="!model.doubaoDraft.cookie.trim()" :loading="model.busy.has('doubao-add')">验证并添加</a-button></div>
  </form>
</template>
