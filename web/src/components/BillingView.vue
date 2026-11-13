<script setup>
import { computed, onMounted, ref, watch } from 'vue';
import { Button as AButton, Empty as AEmpty, Select as ASelect, Option as AOption } from '@arco-design/web-vue';
import { IconDownload, IconRefresh } from '@arco-design/web-vue/es/icon';
import { formatNumber, chinaDate, accountEdition, accountCredits } from '../useAccounts';
import { formatDateTime } from '../dateTime';

// BillingView 积分明细（设计稿 BILLING · 积分明细）：Stats 横向统计条 + 左流水
// （Ledger：Segments 筛选 + 类型/到期列）+ 右侧栏（按账号比例条 + 到期批次分段条）。
// 数据全部来自本地快照（overview + credit_history），打开页面零上游请求。
const props = defineProps({ model: Object });
const creditLimit = ref(500), creditLoading = ref(false), creditEntries = ref([]);
const accountFilter = ref(''), flowFilter = ref('all');
const accounts = computed(() => props.model.accounts);
const historySupported = computed(() => ['workbuddy', 'trae', 'qoder', 'minimax', 'loomy'].includes(props.model.provider));
// 旧账本未标注品牌，均为 WorkBuddy 流水；WorkBuddy 按当前地区账号 UID 隔离。
const providerEntries = computed(() => creditEntries.value.filter(entry =>
  (entry.provider || 'workbuddy') === props.model.provider && accounts.value.some(account => account.uid === entry.uid)
));
const totalCredits = computed(() => accounts.value.reduce((sum, a) => sum + Number(a.credits || 0), 0));
const totalCapacity = computed(() => accounts.value.reduce((sum, a) => sum + Number(a.credits_total || 0), 0));
const balanceUnknown = computed(() => accounts.value.length > 0 && accounts.value.every(a => a.balance_known === false));
const nicknameOf = uid => accounts.value.find(a => a.uid === uid)?.nickname || uid.slice(0, 8);
const nameOf = account => props.model.accountName(account);
watch(() => props.model.platformSelection, () => { accountFilter.value = ''; flowFilter.value = 'all'; loadCredit(); }, { flush: 'sync' });

// 到期批次：overview 快照逐包展开（与首页到期提醒同口径），按到期时间升序。
const expiryRows = computed(() => accounts.value.flatMap(account =>
  (account.credits_expiry_buckets || []).map(bucket => ({
    uid: account.uid, name: nameOf(account), edition: accountEdition(account),
    date: bucket.date, expires_at: bucket.expires_at, remain: Number(bucket.remain || 0),
  })).filter(b => b.remain > 0)
).sort((a, b) => new Date(a.expires_at || a.date) - new Date(b.expires_at || b.date)));
const expiringSoon = computed(() => expiryRows.value.filter(row => daysLeft(row) <= 30));
function daysLeft(row) {
  const target = new Date(row.expires_at || row.date);
  if (Number.isNaN(target.getTime())) return Infinity;
  return Math.max(0, Math.ceil((target - Date.now()) / 86400000));
}
// 每账号最早未来到期日（流水行「到期」列的数据源）。
const earliestExpiryByUid = computed(() => {
  const map = {};
  for (const row of expiryRows.value) if (!map[row.uid]) map[row.uid] = row.date;
  return map;
});
async function loadCredit() {
  creditLoading.value = true;
  try { const data = await props.model.api('credit_history?limit=' + creditLimit.value + '&provider=' + encodeURIComponent(props.model.provider)); creditEntries.value = data.entries || []; }
  catch { creditEntries.value = []; }
  finally { creditLoading.value = false; }
}
const creditRows = computed(() => providerEntries.value
  .filter(entry => !accountFilter.value || entry.uid === accountFilter.value)
  .filter(entry => flowFilter.value === 'all' || (flowFilter.value === 'income' ? entry.delta > 0 : entry.delta < 0)));
const flowCounts = computed(() => ({
  all: providerEntries.value.filter(entry => !accountFilter.value || entry.uid === accountFilter.value).length,
  income: providerEntries.value.filter(entry => (!accountFilter.value || entry.uid === accountFilter.value) && entry.delta > 0).length,
  expense: providerEntries.value.filter(entry => (!accountFilter.value || entry.uid === accountFilter.value) && entry.delta < 0).length,
  expiring: expiringSoon.value.length,
}));
// 本月（北京时间）收入 / 消耗合计。
const monthEntries = computed(() => {
  const month = chinaDate(props.model.now).slice(0, 7);
  return providerEntries.value.filter(entry => chinaDate(entry.time).slice(0, 7) === month);
});
const monthDelta = computed(() => {
  let income = 0, expense = 0, incomeCount = 0, expenseCount = 0;
  for (const entry of monthEntries.value) {
    if (Number(entry.delta) > 0) { income += Number(entry.delta); incomeCount++; }
    else { expense += Number(entry.delta); expenseCount++; }
  }
  return { income, expense, incomeCount, expenseCount };
});
// 按账号对比 + 比例条（余额占总额比）。
const accountSummary = computed(() => {
  const totals = new Map();
  for (const entry of monthEntries.value) {
    const total = totals.get(entry.uid) || { income: 0, expense: 0 };
    if (entry.delta > 0) total.income += Number(entry.delta);
    if (entry.delta < 0) total.expense += Number(entry.delta);
    totals.set(entry.uid, total);
  }
  return accounts.value.map(account => ({
    account, credits: Number(account.credits || 0),
    income: totals.get(account.uid)?.income || 0, expense: totals.get(account.uid)?.expense || 0,
  })).sort((a, b) => b.credits - a.credits);
});
function shareOf(credits) { return totalCredits.value > 0 ? Math.max(3, Math.round(credits / totalCredits.value * 100)) + '%' : '0%'; }
// 到期分段条：30 天内按周（0-7 / 8-15 / 16-23 / 24-30 天）分四段。
const expiryStack = computed(() => {
  const buckets = [0, 0, 0, 0];
  for (const row of expiringSoon.value) {
    const days = daysLeft(row);
    buckets[Math.min(3, Math.floor(days / 8))] += row.remain;
  }
  const total = buckets.reduce((sum, value) => sum + value, 0);
  return buckets.map((value, index) => ({ value, share: total > 0 ? Math.round(value / total * 100) : 0, label: `第 ${index * 8 + 1}-${index * 8 + 8} 天` })).filter(seg => seg.value > 0);
});
const recentDelta = computed(() => {
  const week = Date.now() - 7 * 86400000;
  return providerEntries.value.filter(entry => new Date(entry.time).getTime() >= week).reduce((sum, entry) => sum + Number(entry.delta || 0), 0);
});
function exportCsv() {
  const header = '时间,账号,类型,变动,变动后余额,到期,UID';
  const lines = creditRows.value.map(entry => [
    formatDateTime(entry.time),
    (entry.account || nicknameOf(entry.uid)).replace(/,/g, '，'),
    entry.delta > 0 ? '收入' : '消耗', entry.delta, entry.after,
    earliestExpiryByUid.value[entry.uid] || '', entry.uid,
  ].join(','));
  const blob = new Blob(['\ufeff' + [header, ...lines].join('\n')], { type: 'text/csv;charset=utf-8' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `积分明细_${props.model.providerName}_${chinaDate()}.csv`;
  link.click();
  URL.revokeObjectURL(link.href);
}
function jumpToExpiry() { document.querySelector('.expiry-card')?.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
onMounted(loadCredit);
</script>

<template>
  <section class="billing-view" aria-label="积分明细">
    <header class="page-heading">
      <div class="page-toolbar"><slot name="platform-switcher" /><div class="log-heading-actions">
        <a-button :disabled="!creditRows.length" @click="exportCsv"><template #icon><icon-download /></template>导出明细</a-button>
        <a-button :loading="creditLoading" @click="loadCredit"><template #icon><icon-refresh /></template>刷新积分</a-button>
      </div></div>
    </header>

    <section class="stat-strip">
      <div class="stat-seg"><span>当前余额</span><strong class="accent-text">{{ balanceUnknown ? '--' : formatNumber(totalCredits) }}</strong><small>{{ accounts.length }} 个账号 · 合计<template v-if="totalCapacity">（总额度 {{ formatNumber(totalCapacity) }}）</template></small></div>
      <i class="stat-divider" />
      <div class="stat-seg"><span>30 天内到期</span><strong class="warning-text">{{ formatNumber(expiringSoon.reduce((sum, row) => sum + row.remain, 0)) }}</strong><small>{{ expiringSoon.length }} 个批次<template v-if="expiryRows.length"> · 最近 {{ daysLeft(expiryRows[0]) }} 天后</template></small></div>
      <i class="stat-divider" />
      <div class="stat-seg"><span>本月获得</span><strong class="accent-text">{{ historySupported ? '+' + formatNumber(monthDelta.income) : '—' }}</strong><small>{{ historySupported ? monthDelta.incomeCount + ' 笔收入流水' : '暂无流水数据' }}</small></div>
      <i class="stat-divider" />
      <div class="stat-seg"><span>本月消耗</span><strong>{{ historySupported ? formatNumber(monthDelta.expense) : '—' }}</strong><small>{{ historySupported ? monthDelta.expenseCount + ' 笔消耗流水' : '暂无流水数据' }}</small></div>
    </section>

    <div class="billing-layout">
      <section class="panel-card ledger-card">
        <header class="ledger-head">
          <div class="ledger-title"><h2>积分流水</h2><small>根据已知余额快照记录变化</small></div>
          <div class="filter-tabs" role="group" aria-label="流水分类">
            <button :class="{ active: flowFilter === 'all' }" @click="flowFilter = 'all'">全部 <span>{{ flowCounts.all }}</span></button>
            <button :class="{ active: flowFilter === 'income' }" @click="flowFilter = 'income'">收入 <span>{{ flowCounts.income }}</span></button>
            <button :class="{ active: flowFilter === 'expense' }" @click="flowFilter = 'expense'">消耗 <span>{{ flowCounts.expense }}</span></button>
            <button :class="{ active: flowFilter === 'expiring' }" @click="jumpToExpiry">即将到期 <span>{{ flowCounts.expiring }}</span></button>
          </div>
        </header>
        <a-select v-model="accountFilter" size="small" style="width: 170px" placeholder="全部账号" allow-clear aria-label="按账号筛选"><a-option v-for="account in accounts" :key="account.uid" :value="account.uid">{{ nameOf(account) }}</a-option></a-select>
        <div class="table-wrap ledger-list" tabindex="0" aria-label="积分流水列表"><table>
          <thead><tr><th>时间</th><th>账号</th><th>类型</th><th>变动</th><th>余额</th><th>到期</th></tr></thead>
          <tbody>
            <tr v-for="(entry, index) in creditRows" :key="entry.time + ':' + index">
              <td>{{ formatDateTime(entry.time) }}</td>
              <td>{{ entry.account || nicknameOf(entry.uid) }}</td>
              <td><span :class="['type-pill', entry.delta > 0 ? 'income' : 'expense']">{{ entry.delta > 0 ? '收入' : '消耗' }}</span></td>
              <td :class="entry.delta >= 0 ? 'accent-text' : 'warning-text'">{{ entry.delta >= 0 ? '+' : '' }}{{ formatNumber(entry.delta) }}</td>
              <td>{{ formatNumber(entry.after) }}</td>
              <td>{{ earliestExpiryByUid[entry.uid] || '—' }}</td>
            </tr>
          </tbody>
        </table><a-empty v-if="!creditRows.length && !creditLoading" :description="historySupported ? '暂无积分变动记录（余额刷新后开始记录）' : model.providerName + ' 暂无可用余额流水'" /></div>
        <footer class="ledger-foot"><span>显示最近 {{ creditRows.length }} 条</span><span v-if="historySupported">近 7 天净变动 {{ recentDelta >= 0 ? '+' : '' }}{{ formatNumber(recentDelta) }} · 余额定期刷新后记录变化</span></footer>
      </section>

      <aside class="billing-side">
        <section class="panel-card account-summary">
          <header class="ledger-head"><div class="ledger-title"><h2>按账号</h2><small>余额占比</small></div></header>
          <div class="account-summary-list" tabindex="0" aria-label="账号余额列表">
            <div v-for="row in accountSummary" :key="row.account.uid" class="acct-line">
              <div class="acct-top"><span>{{ nameOf(row.account) }}</span><strong class="accent-text">{{ accountCredits(row.account) }}</strong></div>
              <div class="acct-bar"><i :style="{ width: shareOf(row.credits) }" /></div>
              <small class="benefit-detail"><template v-if="historySupported">本月 {{ row.income ? '+' + formatNumber(row.income) : '--' }} / {{ row.expense ? formatNumber(row.expense) : '--' }} · </template>{{ accountEdition(row.account) }}</small>
            </div>
            <a-empty v-if="!accountSummary.length" description="暂无账号" />
          </div>
          <footer class="acct-net"><span>合计</span><strong>{{ balanceUnknown ? '--' : formatNumber(totalCredits) }}</strong></footer>
        </section>

        <section class="panel-card expiry-card">
          <header class="ledger-head"><div class="ledger-title"><h2>到期批次</h2><small>按到期时间升序 · 随余额后台刷新更新</small></div></header>
          <div class="expiry-big"><strong class="warning-text">{{ formatNumber(expiringSoon.reduce((sum, row) => sum + row.remain, 0)) }}</strong><span>30 天内到期 · {{ expiringSoon.length }} 个批次</span></div>
          <div v-if="expiryStack.length" class="expiry-stack" role="img" aria-label="到期分布">
            <i v-for="(seg, index) in expiryStack" :key="index" :class="'seg-' + index" :style="{ width: seg.share + '%' }" :title="seg.label + ' · ' + formatNumber(seg.value) + ' 分（' + seg.share + '%）'" />
          </div>
          <div class="expiry-legend"><span v-for="(seg, index) in expiryStack" :key="index"><i :class="'seg-' + index" />{{ seg.label }} {{ formatNumber(seg.value) }}</span></div>
          <div class="table-wrap expiry-list" tabindex="0" aria-label="到期批次列表"><table>
            <thead><tr><th>到期日</th><th>账号</th><th>剩余</th><th>天数</th></tr></thead>
            <tbody>
              <tr v-for="row in expiryRows" :key="row.uid + row.date">
                <td>{{ row.date }}</td>
                <td>{{ row.name }}</td>
                <td><strong class="accent-text">{{ formatNumber(row.remain) }}</strong></td>
                <td :class="daysLeft(row) <= 7 ? 'warning-text' : ''">{{ daysLeft(row) }} 天</td>
              </tr>
            </tbody>
          </table><a-empty v-if="!expiryRows.length" description="暂无积分到期记录" /></div>
        </section>
      </aside>
    </div>
  </section>
</template>
