<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Button as AButton, Empty as AEmpty, Message, Switch as ASwitch, Tag as ATag } from '@arco-design/web-vue';
import { IconRefresh, IconCalendar, IconArrowRight, IconPauseCircle } from '@arco-design/web-vue/es/icon';
import { formatHour, formatNumber, clockTime } from '../useAccounts';

// SchedulesView 定时任务（设计稿 AUTOMATION · 定时任务）：签到、巡检、保活与成长
// 任务的统一调度视图。启停与时点归 config.json（网关设置），签到时点权威在
// 「签到设置」页（account-settings.json），此处只读展示并跳转。
const props = defineProps({ model: Object, embedded: Boolean });
const emit = defineEmits(['navigate', 'key']);
const loading = ref(false), saving = ref(false), config = ref(null);
const draft = ref(null), lastRuns = ref({}), pauseConfirm = ref(false);

const families = computed(() => {
  const d = draft.value;
  if (!d) return [];
  return [
    { id: 'checkin', name: '每日签到', desc: 'WorkBuddy + Trae + MiniMax 同批；Qoder 活动轮次顺延至 10:00 起', plan: d.checkin_hours, enabled: d.checkin_enabled, statusKey: 'checkin', readonly: true },
    { id: 'travel', name: '旅行巡检', desc: '一趟派出 + 一趟领奖闭环', plan: d.travel_hours, enabled: d.travel_enabled, key: 'travel', statusKey: 'travel' },
    { id: 'activity', name: '活跃上报', desc: '点亮连登 + 解锁领养前置', plan: d.activity_hours, enabled: d.activity_enabled, key: 'activity', statusKey: 'activity' },
    { id: 'keepalive', name: 'Token 保活', desc: '刷新登录凭证，会话死亡自动禁用', plan: d.keepalive_hours, enabled: d.keepalive_enabled, key: 'keepalive', statusKey: 'keepalive' },
    { id: 'blackcat', name: '夜猫子', desc: '23:00–08:00 窗口对话补足', plan: d.blackcat_hours, enabled: d.blackcat_enabled, key: 'blackcat', statusKey: 'blackcat' },
    { id: 'growth', name: '成长任务', desc: '每日到点自动扫描并执行全部待办', plan: d.growth_hours, enabled: d.growth_enabled, key: 'growth', statusKey: 'growth' },
    { id: 'streak', name: '连登管家', desc: '签到后自动兑换连登档位并抽完抽奖机会', plan: [], enabled: d.streak_enabled, key: 'streak', statusKey: 'streak', follows: 'checkin' },
    { id: 'sweep', name: '任务补领清扫', desc: '周期领取「已达标未领奖」的成长任务奖励（幂等）', interval: d.task_sweep_minutes, enabled: d.task_sweep_enabled, key: 'task_sweep', statusKey: 'sweep' },
    { id: 'balance', name: '余额后台刷新', desc: '只读刷新额度与到期快照，含解冻语义', interval: d.balance_refresh_minutes, enabled: d.balance_refresh_enabled, key: 'balance_refresh', statusKey: 'balance' },
  ];
});
const enabledCount = computed(() => families.value.filter(f => f.enabled).length);
const enabledTotal = computed(() => families.value.length);

// 下次运行：整点任务按北京时间推算，分钟级任务返回 null（无固定时点）。
function nextFire(family) {
  if (!family.enabled || family.interval || family.follows || !family.plan?.length) return null;
  const now = Date.now();
  return Math.min(...family.plan.map(h => {
    const china = new Date(now + 8 * 3600000);
    const day = Date.UTC(china.getUTCFullYear(), china.getUTCMonth(), china.getUTCDate()) - 8 * 3600000;
    const value = day + h * 3600000;
    return value > now ? value : value + 86400000;
  }));
}
function nextRunText(family) {
  if (!family.enabled) return '已停用';
  if (family.interval) return `每 ${family.interval} 分钟`;
  if (family.follows) return '每次签到后';
  const at = nextFire(family);
  if (!at) return '--';
  const sameDay = new Date(at).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date().toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  return `${sameDay ? '今天' : '明天'} ${new Date(at).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })}`;
}
const nextUp = computed(() => {
  const candidates = families.value.map(f => ({ family: f, at: nextFire(f) })).filter(item => item.at);
  if (!candidates.length) return null;
  return candidates.sort((a, b) => a.at - b.at)[0];
});
const nextUpCountdown = computed(() => {
  if (!nextUp.value) return '';
  const minutes = Math.max(1, Math.ceil((nextUp.value.at - Date.now()) / 60000));
  return minutes >= 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分` : `${minutes} 分钟`;
});

// 今日执行 / 失败：从运行日志环形缓冲统计（尽力口径，重启清零）。
const todayRuns = computed(() => {
  const logs = props.model.allTodayLogs;
  const count = re => logs.filter(entry => re.test(entry.text)).length;
  return { checkin: count(/签到|已签/), travel: count(/旅行|巡检/), background: count(/保活|清扫|余额|keepalive|sweep|usage/) };
});
const recentFailures = computed(() => {
  const week = Date.now() - 7 * 86400000;
  return props.model.allLogs.filter(entry => /失败|超时|error|failed|timeout/i.test(entry.text) && new Date(entry.ts).getTime() >= week).length;
});
// 今日时间轴（设计稿右栏 Today）：启用任务族的整点时点按序排列，
// 已到点且 lastRuns 记录晚于该时点 → 「已执行」；最近一个未来时点 → 「下一次」。
const timelineItems = computed(() => {
  const now = Date.now();
  const dayStart = new Date(new Date().toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' })).getTime();
  const items = [];
  for (const family of families.value) {
    if (!family.enabled || family.follows || !family.plan?.length) continue;
    for (const hour of family.plan) {
      const at = dayStart + hour * 3600000;
      items.push({ at, time: formatHour(hour), name: family.name, statusKey: family.statusKey });
    }
  }
  items.sort((a, b) => a.at - b.at);
  let nextMarked = false;
  return items.map(item => {
    const executed = (lastRuns.value[item.statusKey] || 0) * 1000 >= item.at && item.at <= now;
    let status = '已到点';
    if (executed) status = '已执行';
    else if (item.at > now && !nextMarked) { status = '下一次'; nextMarked = true; }
    return { ...item, status };
  });
});

function lastRunText(family) {
  const seconds = lastRuns.value[family.statusKey] || 0;
  if (!seconds) return '本进程内未执行';
  const date = new Date(seconds * 1000);
  const sameDay = date.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date().toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  const yesterday = date.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date(Date.now() - 86400000).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  const time = date.toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false });
  return `${sameDay ? '今天' : yesterday ? '昨天' : date.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit' })} ${time}`;
}
async function load() {
  loading.value = true;
  try {
    const [data, status] = await Promise.all([
      props.model.api('settings/gateway'),
      props.model.api('schedules/status').catch(() => ({ last_runs: {} })),
    ]);
    config.value = data.config || {};
    lastRuns.value = status.last_runs || {};
    const s = config.value.schedule || {};
    draft.value = {
      checkin_enabled: props.model.schedule?.checkin_enabled ?? s.checkin_enabled ?? true,
      checkin_hours: props.model.schedule?.checkin_hours || s.checkin_hours || [9],
      travel_enabled: !!s.travel_enabled, travel_hours: s.travel_hours || [9, 21],
      activity_enabled: !!s.activity_enabled, activity_hours: s.activity_hours || [10],
      keepalive_enabled: !!s.keepalive_enabled, keepalive_hours: s.keepalive_hours || [22],
      blackcat_enabled: !!s.blackcat_enabled, blackcat_hours: s.blackcat_hours || [23],
      growth_enabled: !!s.growth_enabled, growth_hours: s.growth_hours || [1],
      streak_enabled: !!s.streak_enabled,
      task_sweep_enabled: !!s.task_sweep_enabled, task_sweep_minutes: s.task_sweep_minutes ?? 15,
      balance_refresh_enabled: !!s.balance_refresh_enabled, balance_refresh_minutes: s.balance_refresh_minutes ?? 5,
    };
  } catch (cause) { Message.error(cause.message); }
  finally { loading.value = false; }
}
let pollTimer;
onMounted(() => { load(); pollTimer = setInterval(() => { props.model.api('schedules/status').then(data => { lastRuns.value = data.last_runs || {}; }).catch(() => {}); }, 15000); });
onBeforeUnmount(() => clearInterval(pollTimer));
watch(() => props.model.schedule, () => { if (draft.value && !saving.value) draft.value.checkin_hours = props.model.schedule?.checkin_hours || draft.value.checkin_hours; });
async function save() {
  saving.value = true;
  try {
    const d = draft.value;
    await props.model.api('settings/gateway', { method: 'POST', body: JSON.stringify({ schedule: {
      travel_enabled: d.travel_enabled, travel_hours: d.travel_hours,
      activity_enabled: d.activity_enabled, activity_hours: d.activity_hours,
      keepalive_enabled: d.keepalive_enabled, keepalive_hours: d.keepalive_hours,
      blackcat_enabled: d.blackcat_enabled, blackcat_hours: d.blackcat_hours,
      growth_enabled: d.growth_enabled, growth_hours: d.growth_hours,
      streak_enabled: d.streak_enabled,
      task_sweep_enabled: d.task_sweep_enabled, task_sweep_minutes: Number(d.task_sweep_minutes) || 15,
      balance_refresh_enabled: d.balance_refresh_enabled, balance_refresh_minutes: Number(d.balance_refresh_minutes) || 5,
    } }) });
    pauseConfirm.value = false;
    Message.success('定时任务已保存（启停即时热生效）');
  } catch (cause) { Message.error(cause.message); }
  finally { saving.value = false; }
}
function pauseAll() {
  if (!draft.value) return;
  for (const family of families.value) if (family.key) draft.value[family.key + '_enabled'] = false;
  save();
}
</script>

<template>
  <section class="schedules-view">
    <header class="page-heading">
      <div class="log-heading-actions">
        <a-button :loading="loading" @click="load"><template #icon><icon-refresh /></template>刷新</a-button>
        <a-button :disabled="!draft || !families.some(f => f.enabled && f.key)" @click="pauseConfirm = true"><template #icon><icon-pause-circle /></template>全部暂停</a-button>
        <a-button type="primary" :loading="saving" :disabled="!draft" @click="save"><template #icon><icon-calendar /></template>保存设置</a-button>
      </div>
    </header>

    <section class="stat-strip">
      <div class="stat-seg"><span>已启用任务</span><strong>{{ enabledCount }} / {{ enabledTotal }}</strong><small>共 {{ enabledTotal }} 个任务</small></div>
      <i class="stat-divider" />
      <div class="stat-seg"><span>下次运行</span><strong>{{ nextUp ? nextRunText(nextUp.family).replace(/^今天 |^明天 /, '') : '--' }}</strong><small>{{ nextUp ? nextUp.family.name + (nextUpCountdown ? ' · 还有 ' + nextUpCountdown : '') : '所有任务已停用' }}</small></div>
      <i class="stat-divider" />
      <div class="stat-seg"><span>今日执行</span><strong>{{ todayRuns.checkin + todayRuns.travel + todayRuns.background }}</strong><small>签到 {{ todayRuns.checkin }} · 巡检 {{ todayRuns.travel }} · 后台 {{ todayRuns.background }}</small></div>
      <i class="stat-divider" />
      <div class="stat-seg"><span>异常</span><strong :class="recentFailures ? 'warning-text' : ''">{{ recentFailures }}</strong><small>最近日志中失败/超时条目</small></div>
    </section>

    <div class="billing-layout">
      <section class="panel-card">
        <header class="ledger-head">
          <div class="ledger-title"><h2>任务计划</h2><small>启停即时热生效；未启用的任务不会对上游发起任何请求</small></div>
          <a-button size="small" type="text" @click="emit('navigate', 'logs')">查看运行日志<icon-arrow-right /></a-button>
        </header>
        <div class="table-wrap"><table>
          <thead><tr><th>任务</th><th>计划</th><th>上次运行</th><th>下次运行</th><th>状态</th><th>启用</th></tr></thead>
          <tbody>
            <tr v-for="family in families" :key="family.id">
              <td><div class="task-identity"><span class="task-symbol"><icon-calendar /></span><div><strong>{{ family.name }}</strong><small class="benefit-detail">{{ family.desc }}</small></div></div></td>
              <td><div class="schedule-chips"><template v-if="family.interval"><span class="schedule-chip">{{ family.interval }} 分钟</span></template><template v-else-if="family.plan?.length"><span v-for="hour in family.plan" :key="hour" class="schedule-chip">{{ formatHour(hour) }}</span></template><template v-else-if="family.follows"><span class="schedule-chip">跟随签到</span></template><template v-else>—</template></div></td>
              <td>{{ lastRunText(family) }}</td>
              <td>{{ nextRunText(family) }}</td>
              <td><a-tag :color="family.enabled ? 'arcoblue' : 'gray'">{{ family.enabled ? '运行中' : '已停用' }}</a-tag></td>
              <td>
                <a-switch v-if="family.readonly" :model-value="family.enabled" size="small" disabled aria-label="在签到设置页维护" />
                <a-switch v-else-if="draft" v-model="draft[family.key + '_enabled']" size="small" :aria-label="'启用' + family.name" />
              </td>
            </tr>
          </tbody>
        </table><a-empty v-if="!families.length && !loading" description="定时任务配置读取失败，请刷新重试" /></div>
        <footer class="ledger-foot"><span>上次运行为本进程内记录（重启清零）</span><span>旅行 / 活跃 / 保活 / 成长 / 连登不因开启网关自动启用</span></footer>
      </section>

      <aside class="billing-side">
        <section class="panel-card">
          <header class="ledger-head"><div class="ledger-title"><h2>今日</h2><small>启用任务的整点时点 · 北京时间</small></div></header>
          <div v-if="timelineItems.length" class="timeline" role="list" aria-label="今日执行时间轴">
            <div v-for="(item, index) in timelineItems" :key="item.name + item.time" class="timeline-item" role="listitem">
              <time>{{ item.time }}</time>
              <span class="timeline-track"><i :class="['timeline-dot', { done: item.status === '已执行', next: item.status === '下一次' }]" /><i v-if="index < timelineItems.length - 1" class="timeline-line" /></span>
              <span class="timeline-info"><strong>{{ item.name }}</strong><small :class="{ 'accent-text': item.status === '下一次' }">{{ item.status }}</small></span>
            </div>
          </div>
          <a-empty v-else description="当前没有启用的整点任务" />
          <footer class="acct-net"><span>分钟级任务</span><small>{{ families.filter(f => f.enabled && f.interval).map(f => f.name + ' 每 ' + f.interval + ' 分钟').join(' · ') || '无' }}</small></footer>
        </section>
        <section class="panel-card">
          <header class="ledger-head"><div class="ledger-title"><h2>说明</h2></div></header>
          <p class="billing-hint-inline">每日签到的时点与开关以「签到设置」页为准（account-settings.json），本页不重复维护；连登管家跟随每次签到执行。</p>
          <p class="billing-hint-inline">旅行 / 活跃 / 保活 / 成长 / 连登等任务族不因开启网关自动启用；是否覆盖已停用账号等高级开关在「网关 → 网关设置」。</p>
        </section>
      </aside>
    </div>

    <a-modal :visible="pauseConfirm" title="全部暂停定时任务" width="440px" :footer="false" :mask-closable="false" @cancel="pauseConfirm = false">
      <p class="modal-description">将停用旅行巡检、活跃上报、Token 保活、夜猫子、成长任务、连登管家、任务补领清扫与余额后台刷新。</p>
      <p class="remove-notice">每日签到不受影响（在「签到设置」页维护）；停用后各任务族不再对上游发起任何请求，可随时在本页重新开启。</p>
      <div class="modal-actions"><a-button @click="pauseConfirm = false">取消</a-button><a-button type="primary" status="danger" :loading="saving" @click="pauseAll">确认全部暂停</a-button></div>
    </a-modal>
  </section>
</template>
