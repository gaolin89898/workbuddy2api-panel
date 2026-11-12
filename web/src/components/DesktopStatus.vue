<script setup>
import { computed } from 'vue';
import { Button as AButton, Tooltip as ATooltip } from '@arco-design/web-vue';
const props = defineProps({ model: Object });
const clients = computed(() => props.model.desktopClients.filter(c => ['trae','qoder'].includes(props.model.provider) ? c.id === props.model.provider : c.id === 'workbuddy' || c.id === 'workbuddy-global' && props.model.accounts.some(a => a.realm === 'global')));
const quiet = promise => promise?.catch(() => {});
</script>

<template>
  <section v-if="model.provider !== 'minimax'" class="desktop-status" aria-label="客户端账号切换">
    <div class="desktop-status-head"><div><strong>客户端账号</strong><p v-if="model.desktopError">{{ model.desktopError }}</p><template v-else><p v-for="client in clients" :key="client.id">{{ client.name }} · {{ !client.available ? client.reason : client.current_uid ? `${client.running ? '当前' : '上次登录'}：${client.current_name || client.current_uid}` : '尚未识别到登录账号' }}<small v-if="client.current_name && client.current_uid" class="desktop-uid">{{ client.current_uid }}</small></p></template></div><a-tooltip v-if="model.provider === 'trae'" content="读取本机 Trae 当前登录态，添加或关联面板账号，以后即可一键切换"><span><a-button :disabled="model.desktopBusy || !model.desktopClient()?.available || !!model.desktopError" :loading="model.busy.has('desktop-capture')" @click="quiet(model.captureDesktop())">保存当前登录态</a-button></span></a-tooltip></div>
    <p v-if="model.provider === 'trae'" class="desktop-hint">每个账号首次在 Trae 客户端登录后，保存一次登录态，即可从列表切换。</p>
    <p v-else class="desktop-hint">点击账号行的「切换账号」，将重启对应的 {{ model.providerName }} 客户端。</p>
    <p v-if="model.desktopJob" :class="['desktop-progress', { 'danger-text': model.desktopJob.error }]" role="status">{{ model.desktopJob.error || model.desktopJob.phase }}</p>
  </section>
</template>
