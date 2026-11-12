import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { Message } from '@arco-design/web-vue';
import { hostRuntime } from './hostRuntime';

const localStorage = hostRuntime.storage;

export const KEY_STORAGE = 'wb2api.key';
const REMARK_STORAGE = 'wb.accounts.remarks';
const ZONE = 'Asia/Shanghai';

export function chinaDate(value = Date.now()) {
  return new Intl.DateTimeFormat('en-CA', { timeZone: ZONE, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));
}
export function clockTime(value) {
  if (!value || String(value).startsWith('0001-')) return '--';
  return new Date(value).toLocaleTimeString('zh-CN', { timeZone: ZONE, hour12: false });
}
export function formatHour(hour) { return String(hour).padStart(2, '0') + ':00'; }
export function formatNumber(value) { return Number(value || 0).toLocaleString('zh-CN'); }
export function canCheckin(account) { return !account.disabled && !account.enterprise && account.realm !== 'global' && account.checkin_supported !== false; }
export function accountEdition(account) { return account.provider === 'minimax' ? 'MiniMax 国内版' : account.provider === 'qoder' ? 'Qoder 国内版' : account.provider === 'trae' ? 'Trae 国内版' : account.provider === 'loomy' ? 'Loomy 国内版' : account.enterprise ? '企业版' : account.realm === 'global' ? '国际版' : '国内版'; }
export function accountCredits(account) { return account.balance_known === false ? '--' : formatNumber(account.credits); }
export function checkinState(account) {
  if (account.provider === 'qoder') return account.checkin_done ? { text: '本轮已领取', kind: 'success' } : account.disabled ? { text: '已暂停', kind: 'muted' } : { text: '本轮待领取', kind: 'warning' };
  if (account.provider === 'loomy') return account.checkin_done ? { text: '今日已结算', kind: 'success' } : account.disabled ? { text: '已暂停', kind: 'muted' } : { text: '待结算', kind: 'warning' };
  if (account.enterprise || account.realm === 'global' || account.checkin_supported === false) return { text: '不支持签到', kind: 'muted' };
  if (account.checkin_done) return { text: '已签到', kind: 'success' };
  if (account.disabled) return { text: '已暂停', kind: 'muted' };
  return { text: '待签到', kind: 'warning' };
}

export function useAccounts() {
  const workbuddyAccounts = ref([]), traeAccounts = ref([]), qoderAccounts = ref([]), minimaxAccounts = ref([]), loomyAccounts = ref([]), allLogs = ref([]), overview = ref({});
  const provider = ref(['trae', 'qoder', 'minimax', 'loomy'].includes(localStorage.getItem('wb.accounts.provider')) ? localStorage.getItem('wb.accounts.provider') : 'workbuddy');
  const providerName = computed(() => ({ trae: 'Trae', qoder: 'Qoder', minimax: 'MiniMax', loomy: 'Loomy', workbuddy: 'WorkBuddy' })[provider.value]);
  const accounts = computed(() => ({ trae: traeAccounts.value, qoder: qoderAccounts.value, minimax: minimaxAccounts.value, loomy: loomyAccounts.value, workbuddy: workbuddyAccounts.value })[provider.value]);
  const logs = computed(() => allLogs.value.filter(e => provider.value === 'workbuddy' ? !/^(trae|qoder|minimax|loomy):/.test(e.text) : e.text.startsWith(provider.value + ':')));
  const apiPrefix = value => ['trae', 'qoder', 'minimax', 'loomy'].includes(value) ? value + '/' : '';
  const effectiveHours = computed(() => [...new Set((schedule.value?.checkin_hours || []).map(h => provider.value === 'qoder' ? Math.max(10, h) : h))].sort((a, b) => a - b));
  const schedule = ref(null), connected = ref(false), loading = ref(true), error = ref('');
  const desktopClients = ref([]), desktopJob = ref(null), desktopError = ref('');
  const pendingDesktopSwitch = ref(null);
  const capabilities = ref(null);
  let desktopNotice = '';
  let desktopStatusVersion = 0;
  const desktopBusy = computed(() => !!pendingDesktopSwitch.value || !!desktopJob.value && !desktopJob.value.done || busy.has('desktop-switch') || busy.has('desktop-capture'));
  function desktopClient(account) {
    const selected = account?.provider || provider.value;
    if (selected === 'minimax' || selected === 'loomy') return null; // MiniMax / Loomy 暂无本机客户端切换
    const id = ['trae', 'qoder'].includes(selected) ? selected : account?.realm === 'global' ? 'workbuddy-global' : 'workbuddy';
    return desktopClients.value.find(c => c.id === id);
  }
  function isCurrentDesktop(account) {
    if (account.provider === 'qoder' && desktopJob.value && !desktopJob.value.done) return false;
    const client = desktopClient(account);
    return !!client?.running && !!client.current_uid && client.current_uid === (account.account_uid || account.uid);
  }
  function isSwitchingDesktop(account) {
    const pending = pendingDesktopSwitch.value;
    if (pending?.uid === account.uid && pending.provider === (account.provider || 'workbuddy')) return true;
    const job = desktopJob.value;
    return !!job && !job.done && job.client === desktopClient(account)?.id && job.uid === (account.account_uid || account.uid);
  }
  function switchReason(account) {
    if (desktopBusy.value) return '客户端正在切换，请等待完成';
    if (desktopError.value) return desktopError.value;
    const client = desktopClient(account);
    if (!client?.available) return client?.reason || '未检测到本机客户端';
    if (account.enterprise) return '企业账号暂不支持客户端切换';
    if (account.provider === 'qoder' && account.desktop_supported === false) return '请先用浏览器授权此 Qoder 账号，再切换客户端';
    if (account.provider === 'trae' && !client.saved_uids.includes(account.account_uid)) return '首次使用：先在 Trae 登录此账号，再点「保存当前登录态」';
    return '';
  }
  async function refreshDesktop() {
    const version = ++desktopStatusVersion;
    try {
      const data = await api('desktop/status');
      if (version !== desktopStatusVersion) return;
      desktopClients.value = data.clients || [];
      desktopJob.value = data.job;
      desktopError.value = '';
      if (data.job?.done && data.job.id !== desktopNotice) {
        desktopNotice = data.job.id;
        if (data.job.error) Message.error({ content: data.job.error, duration: 10000 });
        else Message.success('客户端账号已切换');
      }
    } catch (cause) { if (version === desktopStatusVersion && cause.name !== 'AbortError') desktopError.value = cause.message; }
  }
  async function switchDesktop(account) {
    if (desktopBusy.value) throw new Error('客户端正在切换，请等待完成');
    return action('desktop-switch', async () => {
      ++desktopStatusVersion;
      pendingDesktopSwitch.value = { uid: account.uid, provider: account.provider || 'workbuddy' };
      try {
        const data = await api('desktop/switch', { method: 'POST', body: JSON.stringify({ provider: ['trae', 'qoder'].includes(account.provider) ? account.provider : 'workbuddy', realm: account.realm || 'cn', uid: account.uid, confirmed: true }) });
        ++desktopStatusVersion;
        desktopJob.value = data.job;
        await refreshDesktop();
      } finally { pendingDesktopSwitch.value = null; }
    });
  }
  async function captureDesktop() {
    return action('desktop-capture', async () => {
      await api('desktop/capture', { method: 'POST', body: JSON.stringify({ provider: provider.value }) });
      Message.success('当前客户端登录态已保存');
      await Promise.all([refreshDesktop(), refreshOverview()]);
    });
  }
  const needsKey = ref(false), busy = reactive(new Set()), now = ref(Date.now());
  const addVisible = ref(false), addTab = ref('login'), realm = ref('cn'), addProvider = ref(provider.value);
  const traeDraft = reactive({ nickname: '', session: '', token: '', device_id: '' });
	const traeExpectedUID = ref('');
  const traeLogin = reactive({ state: '', status: '', error: '', waiting: false, available: null, message: '' });
  const qoderLogin = reactive({ state: '', url: '', error: '', waiting: false });
  const qoderDraft = reactive({ token: '', nickname: '', uid: '' });
  const minimaxDraft = reactive({ credential: '', nickname: '', uid: '', auth_kind: '' });
  const minimaxLogin = reactive({ state: '', url: '', userCode: '', error: '', waiting: false, interval: 3000 });
  const loomyDraft = reactive({ credential: '', nickname: '', uid: '', account_uid: '' });
  let minimaxTimer, minimaxAttempt = 0;
  let qoderTimer, qoderAttempt = 0;
  const login = reactive({ state: '', url: '', status: '', error: '', waiting: false });
  const importFile = ref(null), importResult = ref(null), updatedAt = ref(null), savedAt = ref(null);
  let storedRemarks = {};
  try {
    const parsed = JSON.parse(localStorage.getItem(REMARK_STORAGE) || '{}');
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) storedRemarks = parsed;
  } catch { /* A damaged local note must not prevent account access. */ }
  const remarks = ref(storedRemarks);
  const abort = new AbortController();
  let overviewFlight, pollTimer, refreshTimer, clockTimer, loginAttempt = 0;
  let loginBrowser;
  let traePollTimer, traeLoginAttempt = 0;

  const accountName = account => remarks.value[account.uid] || account.nickname || account.uid;
  function selectProvider(value) {
    provider.value = ['trae', 'qoder', 'minimax', 'loomy'].includes(value) ? value : 'workbuddy';
    localStorage.setItem('wb.accounts.provider', provider.value);
  }
  const eligible = computed(() => accounts.value.filter(canCheckin));
  const signed = computed(() => eligible.value.filter(a => a.checkin_done));
  const pending = computed(() => eligible.value.filter(a => !a.checkin_done));
  // 全平台统一视图：供运行日志、签到设置、活动奖励等不区分平台的页面使用。
  const allAccounts = computed(() => [...workbuddyAccounts.value, ...traeAccounts.value, ...qoderAccounts.value, ...minimaxAccounts.value, ...loomyAccounts.value]);
  const allEligible = computed(() => allAccounts.value.filter(canCheckin));
  const allSigned = computed(() => allEligible.value.filter(a => a.checkin_done));
  const allPending = computed(() => allEligible.value.filter(a => !a.checkin_done));
  const credits = computed(() => accounts.value.reduce((sum, a) => sum + Number(a.credits || 0), 0));
  const totalCredits = computed(() => accounts.value.reduce((sum, a) => sum + Number(a.credits_total || 0), 0));
  const unknownBalances = computed(() => accounts.value.filter(a => a.balance_known === false).length);
  const todayLogs = computed(() => logs.value.filter(e => chinaDate(e.ts) === chinaDate(now.value)));
  const allTodayLogs = computed(() => allLogs.value.filter(e => chinaDate(e.ts) === chinaDate(now.value)));
  const recentActivity = computed(() => logs.value.filter(e => e.ch === 'task').slice(-3).reverse());
  const nextRunFor = hours => {
    if (!schedule.value?.checkin_enabled || !schedule.value.checkin_hours?.length) return null;
    const china = new Date(now.value + 8 * 3600000);
    const day = Date.UTC(china.getUTCFullYear(), china.getUTCMonth(), china.getUTCDate()) - 8 * 3600000;
    const next = Math.min(...hours.map(h => {
      const value = day + h * 3600000;
      return value > now.value ? value : value + 86400000;
    }));
    const minutes = Math.max(1, Math.ceil((next - now.value) / 60000));
    return {
      timestamp: next,
      time: new Date(next).toLocaleTimeString('zh-CN', { timeZone: ZONE, hour: '2-digit', minute: '2-digit', hour12: false }),
      day: chinaDate(next) === chinaDate(now.value) ? '今天' : '明天',
      date: new Date(next).toLocaleDateString('zh-CN', { timeZone: ZONE, year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' }),
      remaining: minutes >= 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分` : `${minutes} 分钟`,
    };
  };
  const nextRun = computed(() => nextRunFor(effectiveHours.value));
  // 统一视图的下次签到：不按 Qoder 顺延，即所有平台中最早的可签到时刻。
  const allNextRun = computed(() => nextRunFor([...new Set(schedule.value?.checkin_hours || [])].sort((a, b) => a - b)));
  const expiryEntries = computed(() => accounts.value.flatMap(account =>
    (account.credits_expiry_buckets || []).map(bucket => ({ ...bucket, uid: account.uid, name: accountName(account) }))
  ).filter(b => Number(b.remain) > 0 && new Date(b.expires_at || b.date).getTime() > now.value)
    .sort((a, b) => new Date(a.expires_at || a.date) - new Date(b.expires_at || b.date)));

  async function api(path, options = {}) {
    const headers = new Headers(options.headers);
    const key = localStorage.getItem(KEY_STORAGE);
    if (key) headers.set('Authorization', 'Bearer ' + key);
    if (typeof options.body === 'string') headers.set('Content-Type', 'application/json');
    const apiBase = hostRuntime.apiBase || new URL(import.meta.env.BASE_URL + 'api/', document.baseURI);
    const response = await hostRuntime.fetchAPI(new URL(path, apiBase), { signal: abort.signal, ...options, headers });
    const data = await response.json().catch(() => ({}));
    if (response.status === 401) { needsKey.value = true; throw new Error('请填写有效的管理密钥'); }
    if (!response.ok) throw new Error(data.error || `请求失败：HTTP ${response.status}`);
    return data;
  }
  async function action(name, callback) {
    if (busy.has(name)) return;
    busy.add(name);
    try { return await callback(); }
    catch (cause) { if (cause.name !== 'AbortError') Message.error(cause.message); throw cause; }
    finally { busy.delete(name); }
  }
  function refreshOverview() {
    if (overviewFlight) return overviewFlight;
    overviewFlight = api('overview').then(data => {
      workbuddyAccounts.value = data.accounts || [];
      traeAccounts.value = data.trae_accounts || [];
      qoderAccounts.value = data.qoder_accounts || [];
      minimaxAccounts.value = data.minimax_accounts || [];
      loomyAccounts.value = data.loomy_accounts || [];
      overview.value = data;
      connected.value = true;
      error.value = '';
      updatedAt.value = Date.now();
    }).catch(cause => {
      connected.value = false;
      error.value = cause.message;
      throw cause;
    }).finally(() => { loading.value = false; overviewFlight = null; });
    return overviewFlight;
  }
  async function refreshSettings() { const data = await api('settings/checkin'); schedule.value = data.settings; }
  async function refreshCapabilities() {
    try { capabilities.value = await api('capabilities'); } catch { /* 连接横幅统一报错；能力缺失按最低集渲染。 */ }
  }
  async function refreshLogs() { const data = await api('logs'); allLogs.value = data.entries || []; }
  async function connect(key) {
    await action('connect', async () => {
      if (key !== undefined) localStorage.setItem(KEY_STORAGE, key.trim());
      await refreshOverview();
      await Promise.all([refreshSettings(), refreshLogs(), refreshDesktop(), refreshCapabilities()]);
      needsKey.value = false;
    });
  }
  // panelLogin 面板账号密码登录：POST auth/login 换会话令牌，存入与密钥相同的
  // 本地槽位（后端同时接受 api_key 与会话令牌），随后建立全部数据连接。
  async function panelLogin(username, password) {
    return action('connect', async () => {
      const data = await api('auth/login', { method: 'POST', body: JSON.stringify({ username, password }) });
      localStorage.setItem(KEY_STORAGE, data.token);
      await refreshOverview();
      await Promise.all([refreshSettings(), refreshLogs(), refreshDesktop(), refreshCapabilities()]);
      needsKey.value = false;
    });
  }
  async function refreshBalances() {
    return action('balances', async () => {
      const selected = provider.value;
      const data = await api(apiPrefix(selected) + 'balance_all', { method: 'POST' });
      if (data.errors?.length) Message.warning(data.errors.join('；'));
      else Message.success('积分刷新完成');
      await refreshOverview();
      await refreshLogs();
    });
  }
  async function checkinAll() {
    return action('checkin-all', async () => {
      await api(apiPrefix(provider.value) + 'checkin_all', { method: 'POST' });
      Message.success('签到已开始');
      await refreshOverview();
      await refreshLogs();
    });
  }
  async function accountAction(account, type) {
    return action(`${account.uid}:${type}`, async () => {
      const result = await api(`${apiPrefix(account.provider)}accounts/${encodeURIComponent(account.uid)}/${type}`, { method: 'POST' });
      if (type === 'checkin' && !result.checkin_done) throw new Error(result.checkin_message || '签到未完成');
      if (result.file_error) Message.warning('账号已移除，但本地凭证文件删除失败：' + result.file_error);
      else if (result.balance_error) Message.warning('签到成功，但积分刷新失败：' + result.balance_error);
      else Message.success({ checkin: '签到成功', balance: '积分已刷新', disable: '账号已停用', revive: '账号已启用', pause: '已暂停选号（保号任务照常）', resume: '已恢复参与选号', remove: '账号已移除' }[type]);
      await refreshOverview();
      await refreshLogs();
    });
  }
  function saveRemark(account, value) {
    remarks.value = { ...remarks.value, [account.uid]: value.trim() };
    localStorage.setItem(REMARK_STORAGE, JSON.stringify(remarks.value));
    Message.success('备注已保存');
  }
  async function saveSchedule(next) {
    return action('schedule', async () => {
      await api('settings/checkin', { method: 'POST', body: JSON.stringify(next) });
      schedule.value = { ...schedule.value, ...next };
      savedAt.value = Date.now();
      Message.success('签到设置已保存');
    });
  }
  function stopLogin() {
    ++loginAttempt;
    clearTimeout(pollTimer);
    loginBrowser?.close();
    loginBrowser = null;
    Object.assign(login, { state: '', url: '', status: '', error: '', waiting: false });
    stopTraeLogin();
    stopQoderLogin();
    stopMiniMaxLogin();
  }
  function stopTraeLogin() {
    ++traeLoginAttempt;
    clearTimeout(traePollTimer);
    const state = traeLogin.state;
    Object.assign(traeLogin, { state: '', status: '', error: '', waiting: false });
    if (state) api('trae/login/cancel?state=' + encodeURIComponent(state), { method: 'POST', keepalive: true, signal: undefined }).catch(() => {});
  }
  async function checkTraeLoginAvailable() {
    try {
      const data = await api('trae/login/available');
      traeLogin.available = Boolean(data.available);
      traeLogin.message = data.message || '';
    } catch (cause) {
      traeLogin.available = false;
      traeLogin.message = cause.message;
    }
  }
  async function startTraeLogin() {
    stopTraeLogin();
    const attempt = traeLoginAttempt;
    return action('trae-login', async () => {
      try {
        const data = await api('trae/login/start', { method: 'POST', body: JSON.stringify({ uid: traeExpectedUID.value }) });
        if (attempt !== traeLoginAttempt || !addVisible.value || addProvider.value !== 'trae') {
          api('trae/login/cancel?state=' + encodeURIComponent(data.state), { method: 'POST' }).catch(() => {});
          return;
        }
        Object.assign(traeLogin, { state: data.state, status: '请在新窗口完成 Trae 登录', waiting: true });
        traePollTimer = setTimeout(() => pollTraeLogin(attempt), 1500);
      } catch (cause) {
        if (attempt === traeLoginAttempt) traeLogin.error = cause.message;
        throw cause;
      }
    });
  }
  async function pollTraeLogin(attempt) {
    if (attempt !== traeLoginAttempt || !traeLogin.state || !addVisible.value) return;
    try {
      const data = await api('trae/login/poll?state=' + encodeURIComponent(traeLogin.state));
      if (attempt !== traeLoginAttempt || !addVisible.value) return;
      if (data.done) {
        if (data.status !== 'success') {
          Object.assign(traeLogin, { waiting: false, error: data.error || '登录未完成，请重试' });
          return;
        }
        closeAdd();
        selectProvider('trae');
        Message.success('Trae 登录成功，账号已添加或更新');
        if (data.warnings?.length) Message.warning(data.warnings.join('；'));
        await refreshOverview();
        await refreshLogs();
      } else {
        traeLogin.status = data.status === 'saving' ? '已获取登录信息，正在验证并保存账号…' : '请在新窗口完成 Trae 登录';
        traePollTimer = setTimeout(() => pollTraeLogin(attempt), 1500);
      }
    } catch (cause) {
      if (attempt === traeLoginAttempt) {
        const state = traeLogin.state;
        Object.assign(traeLogin, { waiting: false, error: cause.message });
        if (state) api('trae/login/cancel?state=' + encodeURIComponent(state), { method: 'POST' }).catch(() => {});
      }
    }
  }
  function openAdd(tab = 'login') {
    stopLogin();
    addTab.value = tab;
    addProvider.value = provider.value;
	traeExpectedUID.value = '';
    Object.assign(traeDraft, { nickname: '', session: '', token: '', device_id: '' });
    Object.assign(qoderDraft, { token: '', nickname: '', uid: '' });
    Object.assign(minimaxDraft, { credential: '', nickname: '', uid: '', auth_kind: '' });
    Object.assign(minimaxLogin, { state: '', url: '', userCode: '', error: '', waiting: false, interval: 3000 });
    Object.assign(loomyDraft, { credential: '', nickname: '', uid: '', account_uid: '' });
    importFile.value = null;
    importResult.value = null;
    addVisible.value = true;
  }
  function closeAdd() { stopLogin(); addVisible.value = false; Object.assign(traeDraft, { nickname: '', session: '', token: '', device_id: '' }); Object.assign(qoderDraft, { token: '', nickname: '', uid: '' }); Object.assign(minimaxDraft, { credential: '', nickname: '', uid: '', auth_kind: '' }); Object.assign(loomyDraft, { credential: '', nickname: '', uid: '', account_uid: '' }); }
  function stopQoderLogin() {
    ++qoderAttempt;
    clearTimeout(qoderTimer);
    const state = qoderLogin.state;
    Object.assign(qoderLogin, { state: '', url: '', error: '', waiting: false });
    if (state) api('qoder/login/cancel?state=' + encodeURIComponent(state), { method: 'POST', keepalive: true, signal: undefined }).catch(() => {});
  }
  function updateQoder(account) {
    openAdd(); addProvider.value = 'qoder';
    qoderDraft.uid = account.uid; qoderDraft.nickname = account.nickname || '';
  }
  async function addQoder() {
    return action('qoder-add', async () => {
      const result = await api('qoder/accounts', { method: 'POST', body: JSON.stringify(qoderDraft) });
      closeAdd(); selectProvider('qoder'); Message.success('Qoder 账号已添加或更新');
      if (result.warnings?.length) Message.warning(result.warnings.join('；'));
      await refreshOverview();
    });
  }
  function updateMiniMax(account) {
    openAdd(); addProvider.value = 'minimax';
    minimaxDraft.uid = account.uid; minimaxDraft.nickname = account.nickname || ''; minimaxDraft.auth_kind = account.auth_kind || '';
  }
  async function addMiniMax() {
    return action('minimax-add', async () => {
      const result = await api('minimax/accounts', { method: 'POST', body: JSON.stringify(minimaxDraft) });
      closeAdd(); selectProvider('minimax'); Message.success('MiniMax 账号已添加或更新');
      if (result.warnings?.length) Message.warning(result.warnings.join('；'));
      await refreshOverview();
      await refreshLogs();
    });
  }
  function stopMiniMaxLogin() {
    ++minimaxAttempt;
    clearTimeout(minimaxTimer);
    const state = minimaxLogin.state;
    Object.assign(minimaxLogin, { state: '', url: '', userCode: '', error: '', waiting: false, interval: 3000 });
    if (state) api('minimax/login/cancel?state=' + encodeURIComponent(state), { method: 'POST', keepalive: true, signal: undefined }).catch(() => {});
  }
  function updateLoomy(account) {
    openAdd(); addProvider.value = 'loomy';
    loomyDraft.uid = account.uid; loomyDraft.nickname = account.nickname || ''; loomyDraft.account_uid = account.account_uid || '';
  }
  async function addLoomy() {
    return action('loomy-add', async () => {
      const result = await api('loomy/accounts', { method: 'POST', body: JSON.stringify(loomyDraft) });
      closeAdd(); selectProvider('loomy'); Message.success('Loomy 账号已添加或更新');
      if (result.warnings?.length) Message.warning(result.warnings.join('；'));
      await refreshOverview();
      await refreshLogs();
    });
  }
  async function importLoomyLocal() {
    return action('loomy-import', async () => {
      const result = await api('loomy/local_import', { method: 'POST' });
      closeAdd(); selectProvider('loomy'); Message.success('Loomy 本机会话已导入');
      if (result.warnings?.length) Message.warning(result.warnings.join('；'));
      await refreshOverview();
      await refreshLogs();
    });
  }
  async function startMiniMaxLogin() {
    stopMiniMaxLogin();
    const attempt = minimaxAttempt;
    return action('minimax-login', async () => {
      try {
        const data = await api('minimax/login/start', { method: 'POST', body: JSON.stringify({ uid: minimaxDraft.uid }) });
        if (attempt !== minimaxAttempt || !addVisible.value || addProvider.value !== 'minimax') {
          api('minimax/login/cancel?state=' + encodeURIComponent(data.state), { method: 'POST' }).catch(() => {});
          return;
        }
        const url = new URL(data.url);
        if (url.protocol !== 'https:') throw new Error('MiniMax 授权地址无效');
        Object.assign(minimaxLogin, { state: data.state, url: url.href, userCode: data.user_code || '', waiting: true, interval: Math.max(2000, (data.interval_sec || 3) * 1000), error: '' });
        minimaxTimer = setTimeout(() => pollMiniMaxLogin(attempt), minimaxLogin.interval);
      } catch (cause) { if (attempt === minimaxAttempt) minimaxLogin.error = cause.message; throw cause; }
    });
  }
  async function pollMiniMaxLogin(attempt) {
    if (attempt !== minimaxAttempt || !minimaxLogin.state || !addVisible.value) return;
    try {
      const result = await api('minimax/login/poll?state=' + encodeURIComponent(minimaxLogin.state));
      if (attempt !== minimaxAttempt || !addVisible.value) return;
      if (result.done) {
        closeAdd(); selectProvider('minimax'); Message.success('MiniMax 授权成功，账号已保存');
        if (result.warnings?.length) Message.warning(result.warnings.join('；'));
        await refreshOverview(); await refreshLogs();
      } else minimaxTimer = setTimeout(() => pollMiniMaxLogin(attempt), minimaxLogin.interval);
    } catch (cause) {
      if (attempt === minimaxAttempt) {
        const state = minimaxLogin.state;
        Object.assign(minimaxLogin, { waiting: false, error: cause.message });
        if (state) api('minimax/login/cancel?state=' + encodeURIComponent(state), { method: 'POST' }).catch(() => {});
      }
    }
  }
  async function startQoderLogin() {
    stopQoderLogin(); const attempt = qoderAttempt;
    return action('qoder-login', async () => {
      try {
        const data = await api('qoder/login/start', { method: 'POST', body: JSON.stringify({ uid: qoderDraft.uid }) });
        if (attempt !== qoderAttempt || !addVisible.value || addProvider.value !== 'qoder') {
          api('qoder/login/cancel?state=' + encodeURIComponent(data.state), { method: 'POST' }).catch(() => {}); return;
        }
        const url = new URL(data.url);
        if (url.origin !== 'https://qoder.cn' || url.pathname !== '/sso/login/aliyun') throw new Error('Qoder 授权地址无效');
        Object.assign(qoderLogin, { state: data.state, url: url.href, waiting: true });
        qoderTimer = setTimeout(() => pollQoderLogin(attempt), 2000);
      } catch (cause) { if (attempt === qoderAttempt) qoderLogin.error = cause.message; throw cause; }
    });
  }
  async function pollQoderLogin(attempt) {
    if (attempt !== qoderAttempt || !qoderLogin.state || !addVisible.value) return;
    try {
      const result = await api('qoder/login/poll?state=' + encodeURIComponent(qoderLogin.state));
      if (attempt !== qoderAttempt || !addVisible.value) return;
      if (result.done) {
        closeAdd(); selectProvider('qoder'); Message.success('Qoder 授权成功，账号已保存');
        if (result.warnings?.length) Message.warning(result.warnings.join('；'));
        await refreshOverview(); await refreshLogs();
      } else qoderTimer = setTimeout(() => pollQoderLogin(attempt), 2000);
    } catch (cause) { if (attempt === qoderAttempt) { qoderLogin.waiting = false; qoderLogin.error = cause.message; } }
  }
  function updateTrae(account) {
    openAdd();
    addProvider.value = 'trae';
    traeDraft.nickname = account.nickname || '';
	traeExpectedUID.value = account.uid;
  }
  async function addTrae() {
    return action('trae-add', async () => {
      const result = await api('trae/accounts', { method: 'POST', body: JSON.stringify(traeDraft) });
      closeAdd();
      selectProvider('trae');
      Message.success('Trae 账号已添加或更新');
      if (result.warnings?.length) Message.warning(result.warnings.join('；'));
      await refreshOverview();
      await refreshLogs();
    });
  }
  async function importTrae(file, password) {
    return action('trae-import', async () => {
      if (!file || file.size > 8 * 1024 * 1024) throw new Error('请选择不超过 8 MB 的 TraeTools JSON 文件');
      const body = new FormData();
      body.append('file', file);
      body.append('password', password || '');
      const result = await api('trae/import', { method: 'POST', body });
      selectProvider('trae');
      Message.success(`Trae 导入完成：新增 ${result.imported} 个，更新 ${result.updated} 个`);
      await refreshOverview();
      await refreshLogs();
      return result;
    });
  }
  async function startLogin() {
    if (busy.has('login')) return;
    stopLogin();
    const attempt = loginAttempt;
    await action('login', async () => {
      let browser;
      try {
        browser = loginBrowser = hostRuntime.prepareBrowserLogin();
        const data = await api('login/start', { method: 'POST', body: JSON.stringify({ realm: realm.value }) });
        if (attempt !== loginAttempt || !addVisible.value) { browser.close(); return; }
        const url = new URL(data.url);
        if (url.protocol !== 'https:') throw new Error('授权地址无效');
        Object.assign(login, { state: data.state, url: url.href, status: '等待浏览器授权', waiting: true });
        pollTimer = setTimeout(() => pollLogin(attempt), 3000);
        browser.open(url.href);
      } catch (cause) { browser?.close(); if (attempt === loginAttempt) login.error = cause.message; throw cause; }
      finally { if (loginBrowser === browser) loginBrowser = null; }
    });
  }
  async function pollLogin(attempt) {
    if (attempt !== loginAttempt || !login.state || !addVisible.value) return;
    try {
      const data = await api('login/poll?state=' + encodeURIComponent(login.state));
      if (attempt !== loginAttempt || !addVisible.value) return;
      if (data.done) {
        closeAdd();
        Message.success('账号已添加');
        if (data.checkin_message) Message.warning(data.checkin_message);
        await refreshOverview();
        await refreshLogs();
      } else pollTimer = setTimeout(() => pollLogin(attempt), 3000);
    } catch (cause) {
      if (attempt === loginAttempt) Object.assign(login, { waiting: false, error: cause.message });
    }
  }
  async function importAccounts() {
    return action('import', async () => {
      if (!importFile.value) throw new Error('请选择 Cockpit JSON 文件');
      if (importFile.value.size > 32 * 1024 * 1024) throw new Error('文件不能超过 32 MB');
      const body = new FormData();
      body.append('file', importFile.value);
      importResult.value = await api('import/cockpit', { method: 'POST', body });
      importFile.value = null;
      Message.success(`已导入 ${importResult.value.imported || 0} 个账号`);
      await refreshOverview();
      await refreshLogs();
    });
  }

  onMounted(() => {
    connect().catch(() => {});
    clockTimer = setInterval(() => { now.value = Date.now(); }, 30000);
    refreshTimer = setInterval(() => {
      if (document.hidden || needsKey.value) return;
      Promise.allSettled([refreshOverview(), refreshLogs(), refreshDesktop()]);
    }, 5000);
  });
  onBeforeUnmount(() => { stopLogin(); clearInterval(refreshTimer); clearInterval(clockTimer); abort.abort(); });
  return {
	api,
    accounts, allLogs, logs, overview, schedule, connected, loading, error, needsKey, busy, now, capabilities,
    desktopClients, desktopJob, desktopError, desktopBusy, desktopClient, isCurrentDesktop, isSwitchingDesktop, switchReason, switchDesktop, captureDesktop,
    provider, providerName, selectProvider, workbuddyAccounts, traeAccounts, addProvider, traeDraft, traeExpectedUID, addTrae, importTrae, updateTrae,
    qoderAccounts, qoderLogin, qoderDraft, startQoderLogin, stopQoderLogin, addQoder, updateQoder, effectiveHours,
    minimaxAccounts, minimaxDraft, addMiniMax, updateMiniMax, minimaxLogin, startMiniMaxLogin, stopMiniMaxLogin,
    loomyAccounts, loomyDraft, addLoomy, importLoomyLocal, updateLoomy,
    traeLogin, checkTraeLoginAvailable, startTraeLogin, stopTraeLogin,
    remarks, accountName, eligible, signed, pending, credits, totalCredits, unknownBalances, todayLogs,
    allAccounts, allEligible, allSigned, allPending, allTodayLogs, allNextRun,
    recentActivity, nextRun, expiryEntries, addVisible, addTab, realm, login,
    importFile, importResult, updatedAt, savedAt, connect, refreshOverview, refreshLogs,
    refreshBalances, checkinAll, accountAction, saveRemark, saveSchedule,
    openAdd, closeAdd, startLogin, importAccounts, resetLogin: stopLogin, panelLogin,
  };
}
