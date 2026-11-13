<script setup>
import { computed } from 'vue';
import { Option as AOption, Select as ASelect } from '@arco-design/web-vue';
import workbuddyIcon from '../assets/platform-workbuddy.png';
import traeIcon from '../assets/platform-trae.png';
import qoderIcon from '../assets/platform-qoder.png';
import minimaxIcon from '../assets/platform-minimax.png';
import loomyIcon from '../assets/platform-loomy.png';
import doubaoIcon from '../assets/platform-doubao.png';
import antigravityIcon from '../assets/platform-antigravity.svg';

const props = defineProps({ model: Object });
const platforms = [
  { id: 'workbuddy-cn', name: 'WorkBuddy 国内版', icon: workbuddyIcon },
  { id: 'workbuddy-global', name: 'WorkBuddy 国际版', icon: workbuddyIcon },
  { id: 'trae', name: 'Trae', icon: traeIcon },
  { id: 'qoder', name: 'Qoder', icon: qoderIcon },
  { id: 'minimax', name: 'MiniMax', icon: minimaxIcon },
  { id: 'loomy', name: 'Loomy', icon: loomyIcon },
  { id: 'antigravity', name: 'Antigravity', icon: antigravityIcon },
  { id: 'doubao', name: '豆包', icon: doubaoIcon },
];
const platformById = Object.fromEntries(platforms.map(item => [item.id, item]));
const counts = computed(() => ({
  'workbuddy-cn': props.model.workbuddyAccounts.filter(account => account.realm !== 'global').length,
  'workbuddy-global': props.model.workbuddyAccounts.filter(account => account.realm === 'global').length,
  trae: props.model.traeAccounts.length,
  qoder: props.model.qoderAccounts.length,
  minimax: props.model.minimaxAccounts.length,
  loomy: props.model.loomyAccounts.length,
  antigravity: props.model.antigravityAccounts.length,
  doubao: props.model.doubaoAccounts.length,
}));
</script>

<template>
  <a-select :model-value="model.platformSelection" class="platform-select" aria-label="账号平台" @change="model.selectProvider">
    <template #label="{ data }"><span class="platform-option"><img class="platform-mark" :src="platformById[data.value]?.icon" alt="" /><span>{{ platformById[data.value]?.name }}</span><span class="platform-count">{{ counts[data.value] }}</span></span></template>
    <a-option v-for="item in platforms" :key="item.id" :value="item.id" :label="item.name"><span class="platform-option"><img class="platform-mark" :src="item.icon" alt="" /><span>{{ item.name }}</span><span class="platform-count">{{ counts[item.id] }}</span></span></a-option>
  </a-select>
</template>
