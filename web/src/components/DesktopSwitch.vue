<script setup>
import { computed } from 'vue';
import { Button as AButton, Tooltip as ATooltip } from '@arco-design/web-vue';
const props = defineProps({ model: Object, account: Object });
defineEmits(['switch']);
const switching = computed(() => props.account.provider === 'qoder' && props.model.isSwitchingDesktop(props.account));
</script>

<template>
  <template v-if="model.desktopClient(account)">
    <span v-if="model.isCurrentDesktop(account)" class="desktop-current"><span class="status-dot" />当前账号</span>
    <a-tooltip v-else :content="model.switchReason(account) || '退出并重启客户端，切换到此账号'">
      <span><a-button type="text" size="mini" :class="{ 'qoder-switch-action': account.provider === 'qoder' }" :loading="switching" :aria-busy="switching" :disabled="model.desktopBusy || !!model.switchReason(account)" @click="$emit('switch', account)">{{ switching ? '切换中' : '切换账号' }}</a-button></span>
    </a-tooltip>
  </template>
</template>
