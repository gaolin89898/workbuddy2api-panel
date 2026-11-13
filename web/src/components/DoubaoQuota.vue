<script setup>
import { Progress as AProgress } from '@arco-design/web-vue';
import { accountCredits, formatNumber } from '../useAccounts';

defineProps({ account: Object });
const date = value => new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
const usedRatio = quota => quota.total > 0 ? Math.min(1, Math.max(0, quota.used / quota.total)) : 0;
</script>

<template>
  <div class="doubao-quota-list">
    <small v-if="account.plan" class="quota-plan">{{ account.plan }}</small>
    <div v-for="(quota, index) in account.quotas || []" :key="index" class="doubao-quota">
      <div class="quota-heading"><span>{{ quota.name }}</span><span>剩余 <b>{{ formatNumber(quota.remaining) }}</b></span></div>
      <small>已用 {{ formatNumber(quota.used) }} / {{ formatNumber(quota.total) }}</small>
      <a-progress :percent="usedRatio(quota)" :show-text="false" size="small" :stroke-width="3" :color="account.disabled ? 'var(--muted)' : 'var(--accent)'" />
      <small v-if="quota.expires_at">{{ date(quota.expires_at) }} 到期</small>
      <small v-else-if="quota.starts_at">{{ date(quota.starts_at) }} 开始</small>
      <small v-else-if="quota.kind === 'subscription'">开始使用后计时</small>
    </div>
    <div v-if="!account.quotas?.length" class="credit-amount"><b>{{ accountCredits(account) }}</b><small>{{ account.balance_known ? '暂无活动额度' : '尚未获取额度' }}</small></div>
    <small v-if="account.balance_updated_at && account.balance_known" class="quota-updated">{{ account.last_error ? '上次成功' : '更新于' }} {{ date(account.balance_updated_at) }}</small>
  </div>
</template>

<style scoped>
.doubao-quota-list { display: grid; gap: 12px; min-width: 200px; width: 100%; }
.doubao-quota { display: grid; gap: 4px; }
.quota-heading { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; font-size: 12px; }
.quota-heading b { color: var(--text); font-size: 16px; font-variant-numeric: tabular-nums; }
.doubao-quota-list small { color: var(--muted); font-size: 11px; line-height: 1.5; }
.quota-plan { color: var(--accent) !important; }
.quota-updated { margin-top: 2px; }
</style>
