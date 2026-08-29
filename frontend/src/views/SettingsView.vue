<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Bell, CalendarClock, CheckCircle2, CircleAlert, Cloud, Database, ExternalLink, Eye, EyeOff, FolderGit2, GitCommit, Globe2, KeyRound, LoaderCircle, LogIn, Monitor, PackageOpen, RefreshCw, Save, Send, ShieldCheck, Sparkles, Trash2, WifiOff } from '@lucide/vue'
import { useRoute } from 'vue-router'
import { api } from '../api/client'
import { subscribeRealtime } from '../composables/useRealtime'
import { useToast } from '../composables/useToast'
import { useUpdates } from '../composables/useUpdates'

const loading = ref(true)
const route = useRoute()
const saving = ref('')
const dataPath = ref('')
const runtime = ref({})
const showPublicAPIKey = ref(false)
let runtimeRefreshTimer
let realtimeRefreshTimer
let realtimeUnsubscribe = () => {}
const form = reactive({
  enable_mail_watcher: false,
  enable_apple_keep_alive: false,
  enable_public_mailbox_api: false,
  enable_public_code_page: false,
  enable_web_code_sync: false,
  enable_web_manual_mail_sync: true,
  enable_web_background_mail: true,
  enable_web_remote_mail_cleanup: true,
  public_api_key: '',
  apple_account_module_ready: true,
  server_chan_send_key: '',
  server_chan_hide_ip: true,
  notify_admin_login: false,
  notify_account_login_state_offline: false,
})
const { success, error: showError } = useToast()
const { updateState, showChecking, loadUpdates } = useUpdates()
const publicAPIKeyReady = computed(() => Boolean(String(form.public_api_key || '').trim() || runtime.value.config_api_key_configured))
const publicAPIKeySourceText = computed(() => {
  if (String(form.public_api_key || '').trim()) return '系统设置'
  if (runtime.value.config_api_key_configured) return 'config.json'
  return '尚未设置'
})
const mailWatcherStatusText = computed(() => {
  const status = runtime.value.mail_watcher_status || {}
  if (!runtime.value.mail_watcher_available) return '配置已关闭'
  if (!form.enable_mail_watcher) return '未开启'
  if (!status.running) return '启动中'
  if (!status.group_count) return form.enable_web_background_mail ? '等待读信账号' : '等待 IMAP 账号'
  if (status.last_error) return '同步异常，请查看日志'
  if (!status.connected_worker_count && status.worker_count && status.last_idle_error) return form.enable_web_background_mail ? 'IMAP 异常｜Web 兜底中' : 'IMAP 连接异常'
  const web = form.enable_web_background_mail ? status.web_polling_group_count || 0 : '关'
  return `IMAP ${status.connected_worker_count || 0}/${status.worker_count || 0}｜Web ${web}｜同步 ${status.synced_messages || 0}`
})
const mailWatcherStatusClass = computed(() => {
  const status = runtime.value.mail_watcher_status || {}
  if (status.last_error || (!status.connected_worker_count && status.last_idle_error)) return 'text-rose-500'
  if (form.enable_mail_watcher && status.running && status.group_count) return 'text-emerald-600 dark:text-emerald-300'
  return 'text-amber-500'
})
const databaseStatus = computed(() => runtime.value.database_status || {})
const serverChanReady = computed(() => Boolean(String(form.server_chan_send_key || '').trim() || runtime.value.server_chan_configured))
const serverChanKeyPlaceholder = '输入 SCT 开头的 SendKey'

function formatBytes(value) {
  const bytes = Number(value || 0)
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function notify(text, isError = false) {
  if (isError) showError(text)
  else success(text)
}

function generatePublicAPIKey() {
  const bytes = new Uint8Array(24)
  window.crypto.getRandomValues(bytes)
  let binary = ''
  for (const value of bytes) binary += String.fromCharCode(value)
  form.public_api_key = `ipm_${window.btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')}`
  showPublicAPIKey.value = true
  notify('公共 API Key 已生成，请保存系统设置')
}

async function load(options = {}) {
  const silent = Boolean(options.silent)
  if (!silent) loading.value = true
  try {
    const settingsData = await api('/api/settings')
    Object.assign(form, settingsData.settings || {})
    dataPath.value = settingsData.data_path || ''
    runtime.value = settingsData.runtime || {}
  } catch (err) {
    notify(err.message, true)
  } finally {
    if (!silent) loading.value = false
  }
}

async function saveSystem() {
  saving.value = 'system'
  try {
    const data = await api('/api/settings', { method: 'PUT', body: JSON.stringify(form) })
    Object.assign(form, data.settings || {})
    Object.assign(runtime.value, data.runtime || {})
    runtime.value.api_configured = Boolean(String(form.public_api_key || '').trim() || runtime.value.config_api_key_configured)
    runtime.value.api_key_source = String(form.public_api_key || '').trim() ? 'system_settings' : (runtime.value.config_api_key_configured ? 'config' : '')
    notify('系统设置已保存')
  } catch (err) { notify(err.message, true) } finally { saving.value = '' }
}

async function testServerChan() {
  if (saving.value) return
  saving.value = 'server-chan-test'
  try {
    const data = await api('/api/server-chan/test', {
      method: 'POST',
      body: JSON.stringify({
        send_key: form.server_chan_send_key,
        hide_ip: form.server_chan_hide_ip,
      }),
    })
    notify(data.message || '测试推送已加入 Server 酱队列')
  } catch (err) {
    notify(err.message, true)
  } finally {
    saving.value = ''
  }
}

async function refreshRuntime() {
  try {
    const settingsData = await api('/api/settings')
    runtime.value = settingsData.runtime || runtime.value
  } catch {
    return
  }
}

async function runDatabaseAction(action) {
  if (saving.value) return
  saving.value = `database-${action}`
  try {
    const data = await api(`/api/database/${action}`, { method: 'POST', body: '{}' })
    if (action === 'backup') notify(`数据库备份已创建：${data.path}`)
    else if (action === 'check') notify(`数据库完整性检查：${data.result}`)
    else notify('数据库空间整理完成')
    const statusData = await api('/api/database/status')
    runtime.value.database_status = statusData.database || runtime.value.database_status
  } catch (err) {
    notify(err.message, true)
  } finally {
    saving.value = ''
  }
}

function scheduleRealtimeRefresh(change) {
	if (change.resource === 'mailwatcher' && change.payload?.data) {
		runtime.value.mail_watcher_status = change.payload.data
		return
	}
  window.clearTimeout(realtimeRefreshTimer)
  realtimeRefreshTimer = window.setTimeout(() => {
    if (change.resource === 'settings') void load({ silent: true })
    else void refreshRuntime()
  }, 120)
}

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
}

function shortCommit(value) {
  const commit = String(value || '').trim()
  if (!commit || commit === 'unknown') return '未写入'
  return commit.slice(0, 12)
}

async function checkForUpdates() {
  try {
    const status = await loadUpdates(true)
    if (status?.error) {
      showError(status.error)
    } else if (status?.update_available) {
      success('发现新的项目版本或源码提交')
    } else {
      success('检查完成，当前已经是最新版本')
    }
  } catch (err) {
    showError(err.message)
  }
}

async function scrollToVersionCard() {
  if (route.hash !== '#version-updates') return
  await nextTick()
  window.setTimeout(() => document.querySelector('#version-updates')?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 50)
}

watch(() => route.hash, scrollToVersionCard)

onMounted(async () => {
  await Promise.allSettled([load(), loadUpdates()])
  scrollToVersionCard()
  realtimeUnsubscribe = subscribeRealtime(['settings', 'mailwatcher', 'apple-session', 'apple-account', 'mailbox'], scheduleRealtimeRefresh)
  runtimeRefreshTimer = window.setInterval(refreshRuntime, 30000)
})

onBeforeUnmount(() => {
  window.clearInterval(runtimeRefreshTimer)
  window.clearTimeout(realtimeRefreshTimer)
  realtimeUnsubscribe()
})
</script>

<template>
  <div class="settings-page">
    <div v-if="loading" class="settings-loading"><LoaderCircle :size="18" class="animate-spin" />正在加载系统设置</div>
    <template v-else>
      <form class="panel settings-workbench" @submit.prevent="saveSystem">
        <header class="settings-command-bar"><div><span><Database :size="16" /></span><div><h2>系统设置</h2><p>本地数据、后台能力和公共访问</p></div></div><button class="primary-button settings-save-button" :disabled="saving === 'system'"><LoaderCircle v-if="saving === 'system'" :size="14" class="animate-spin" /><Save v-else :size="14" />{{ saving === 'system' ? '保存中' : '保存系统设置' }}</button></header>
        <div class="settings-form-body">
        <section class="settings-local-data">
          <h3 class="section-title flex items-center gap-2"><Database :size="16" />本地数据</h3>
          <div class="grid gap-4">
            <div class="database-maintenance">
              <div class="database-stats">
                <span><small>数据库</small><strong>{{ formatBytes(databaseStatus.database_bytes) }}</strong></span>
                <span><small>WAL</small><strong>{{ formatBytes(databaseStatus.wal_bytes) }}</strong></span>
                <span><small>变更日志</small><strong>{{ databaseStatus.change_log_count || 0 }} 条</strong></span>
                <span><small>结构版本</small><strong>v{{ databaseStatus.schema_version || '-' }}</strong></span>
              </div>
              <div class="database-actions">
                <span class="database-storage-meta">
                  <span>SQLite 数据库：<code>{{ dataPath || 'data/app.db' }}</code></span>
                  <span>邮件保留 {{ runtime.database_message_retention_days || 90 }} 天；自动备份最多 {{ runtime.database_backup_retention_count || 3 }} 份：<code>{{ runtime.database_backup_dir || '-' }}</code></span>
                </span>
                <div>
                  <button type="button" class="secondary-button" :disabled="Boolean(saving)" @click="runDatabaseAction('check')"><LoaderCircle v-if="saving === 'database-check'" :size="14" class="animate-spin" /><ShieldCheck v-else :size="14" />完整性检查</button>
                  <button type="button" class="secondary-button" :disabled="Boolean(saving)" @click="runDatabaseAction('backup')"><LoaderCircle v-if="saving === 'database-backup'" :size="14" class="animate-spin" /><Database v-else :size="14" />立即备份</button>
                  <button type="button" class="secondary-button" :disabled="Boolean(saving)" @click="runDatabaseAction('optimize')"><LoaderCircle v-if="saving === 'database-optimize'" :size="14" class="animate-spin" /><RefreshCw v-else :size="14" />整理空间</button>
                </div>
              </div>
            </div>
          </div>
        </section>
        <section class="settings-public-access">
          <h3 class="section-title flex items-center gap-2"><Globe2 :size="16" />公共访问</h3>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="settings-access-option">
              <span class="settings-access-option-icon"><Globe2 :size="16" /></span>
              <span class="settings-access-option-copy"><strong>公共取号 API</strong><small>开放取号和批量查询接口，需 API Key。</small></span>
              <input v-model="form.enable_public_mailbox_api" class="detail-switch" type="checkbox" />
            </label>
            <label class="settings-access-option">
              <span class="settings-access-option-icon"><KeyRound :size="16" /></span>
              <span class="settings-access-option-copy"><strong>公共邮箱取码页面</strong><small>输入邮箱即可获取验证码并查看邮件。</small></span>
              <input v-model="form.enable_public_code_page" class="detail-switch" type="checkbox" />
            </label>
          </div>
        </section>
        <section class="settings-background-capabilities">
          <h3 class="section-title flex items-center gap-2"><ShieldCheck :size="16" />后台能力</h3>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="settings-capability-option">
              <span class="settings-capability-option-icon"><Monitor :size="16" /></span>
              <span class="settings-capability-option-copy"><strong>邮件后台监听</strong><small>IMAP IDLE 收信，Web API 断线兜底。</small><small :class="mailWatcherStatusClass" class="mt-1 font-semibold">{{ mailWatcherStatusText }}</small></span>
              <input v-model="form.enable_mail_watcher" class="detail-switch" type="checkbox" :disabled="!runtime.mail_watcher_available" />
            </label>
            <label class="settings-capability-option">
              <span class="settings-capability-option-icon"><RefreshCw :size="16" /></span>
              <span class="settings-capability-option-copy"><strong>Apple 登录态保活</strong><small>基础 {{ Math.round((runtime.apple_keep_alive_ms || 180000) / 60000) }} 分钟；每 30 秒扫描并在每轮重新随机 ±{{ runtime.apple_keep_alive_jitter_percent ?? 15 }}%</small></span>
              <input v-model="form.enable_apple_keep_alive" class="detail-switch" type="checkbox" :disabled="!runtime.apple_keep_alive_available" />
            </label>
          </div>
        </section>
        <section class="settings-web-api">
          <h3 class="section-title flex items-center gap-2"><Cloud :size="16" />iCloud Web API</h3>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="settings-capability-option">
              <span class="settings-capability-option-icon"><KeyRound :size="16" /></span>
              <span class="settings-capability-option-copy"><strong>Web API 取码与邮件刷新</strong><small>后台与公共取码、公共页面邮件刷新时使用 Web API 补查；默认关闭。</small></span>
              <input v-model="form.enable_web_code_sync" class="detail-switch" type="checkbox" />
            </label>
            <label class="settings-capability-option">
              <span class="settings-capability-option-icon"><RefreshCw :size="16" /></span>
              <span class="settings-capability-option-copy"><strong>Web API 手动邮件同步</strong><small>用于表格、详情和全部已有邮箱的邮件补查与回退。</small></span>
              <input v-model="form.enable_web_manual_mail_sync" class="detail-switch" type="checkbox" />
            </label>
            <label class="settings-capability-option">
              <span class="settings-capability-option-icon"><Monitor :size="16" /></span>
              <span class="settings-capability-option-copy"><strong>Web API 后台邮件监听</strong><small>用于首次扫描、IMAP IDLE 补查及低频轮询。</small></span>
              <input v-model="form.enable_web_background_mail" class="detail-switch" type="checkbox" />
            </label>
            <label class="settings-capability-option">
              <span class="settings-capability-option-icon"><Trash2 :size="16" /></span>
              <span class="settings-capability-option-copy"><strong>Web API 远端邮件操作</strong><small>允许移动邮件、清空废纸篓、云端清理及彻底删除邮箱。</small></span>
              <input v-model="form.enable_web_remote_mail_cleanup" class="detail-switch" type="checkbox" />
            </label>
          </div>
        </section>
        <section class="settings-server-chan">
          <div class="server-chan-card">
            <header class="server-chan-heading">
              <div class="server-chan-title">
                <span><Bell :size="18" /></span>
                <div><h3>Server 酱消息推送</h3><p>通过 sct.ftqq.com 把关键运行事件推送到默认微信消息通道。</p></div>
              </div>
            </header>
            <div class="server-chan-body">
              <label class="form-group server-chan-key-field">
                <span class="form-label">SendKey</span>
                <span class="server-chan-key-wrap"><span class="server-chan-key-icon"><KeyRound :size="16" /></span><input v-model.trim="form.server_chan_send_key" class="field font-mono" type="text" autocomplete="off" maxlength="180" :placeholder="serverChanKeyPlaceholder" /></span>
                <small class="form-help">SendKey 默认显示，并使用本地数据库加密保存；推送使用 Server 酱网站配置的默认微信消息通道。</small>
              </label>
              <div class="server-chan-options">
                <label class="server-chan-option"><span class="server-chan-option-icon"><LogIn :size="16" /></span><span class="server-chan-option-copy"><strong>后台登录通知</strong><small>管理员成功登录后推送账号、时间、访问地址和浏览器信息。</small></span><input v-model="form.notify_admin_login" class="detail-switch" type="checkbox" /></label>
                <label class="server-chan-option"><span class="server-chan-option-icon"><WifiOff :size="16" /></span><span class="server-chan-option-copy"><strong>账号与登录态掉线通知</strong><small>Apple Account、iCloud Web 或 IMAP 由正常转为异常时推送，标题会显示具体 Apple 账号；发送额度由填写的 SendKey 套餐决定，本地不限制条数。</small></span><input v-model="form.notify_account_login_state_offline" class="detail-switch" type="checkbox" /></label>
                <label class="server-chan-option"><span class="server-chan-option-icon"><ShieldCheck :size="16" /></span><span class="server-chan-option-copy"><strong>隐藏调用 IP</strong><small>向 Server 酱提交 <code>noip=1</code>，消息中不显示本服务的外网调用 IP。</small></span><input v-model="form.server_chan_hide_ip" class="detail-switch" type="checkbox" /></label>
              </div>
            </div>
            <footer class="server-chan-footer">
              <span :class="serverChanReady ? 'is-ready' : ''"><i></i>{{ serverChanReady ? '推送凭据已就绪' : '等待配置 SendKey' }}</span>
              <div><a href="https://sct.ftqq.com/" target="_blank" rel="noopener noreferrer">Server 酱控制台<ExternalLink :size="12" /></a><button type="button" class="secondary-button" :disabled="Boolean(saving) || !serverChanReady" @click="testServerChan"><LoaderCircle v-if="saving === 'server-chan-test'" :size="14" class="animate-spin" /><Send v-else :size="14" />{{ saving === 'server-chan-test' ? '提交中' : '发送测试' }}</button></div>
            </footer>
          </div>
        </section>
        <section class="settings-public-key">
          <div class="mt-4 rounded-xl border border-slate-200 bg-slate-50 p-4 dark:border-slate-700 dark:bg-slate-800">
            <div class="flex items-start justify-between gap-3"><div><h4 class="flex items-center gap-2 text-sm font-black"><KeyRound :size="15" class="text-emerald-500" />公共取号 API Key</h4><p class="mt-1 text-xs leading-5 text-slate-400">外部调用取号、批量查询接口时使用；来源：{{ publicAPIKeySourceText }}。</p></div><span :class="publicAPIKeyReady ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300' : 'bg-amber-100 text-amber-700 dark:bg-amber-950/60 dark:text-amber-300'" class="shrink-0 rounded-full px-2.5 py-1 text-[10px] font-bold">{{ publicAPIKeyReady ? '已配置' : '待设置' }}</span></div>
            <div class="settings-api-key-row">
              <span class="field-wrap"><KeyRound :size="17" class="field-icon" /><input v-model.trim="form.public_api_key" class="field field-leading field-trailing font-mono text-xs" :type="showPublicAPIKey ? 'text' : 'password'" autocomplete="new-password" :placeholder="runtime.config_api_key_configured ? '留空继续使用 config.json 中的 api_key' : '输入或点击右侧按钮生成'" /><button type="button" class="absolute right-3 top-1/2 z-10 -translate-y-1/2 rounded-lg p-1 text-slate-400 transition hover:text-slate-600 dark:hover:text-slate-200" :title="showPublicAPIKey ? '隐藏公共 API Key' : '显示公共 API Key'" @click="showPublicAPIKey = !showPublicAPIKey"><EyeOff v-if="showPublicAPIKey" :size="17" /><Eye v-else :size="17" /></button></span>
              <button type="button" class="secondary-button settings-key-generate" @click="generatePublicAPIKey"><Sparkles :size="14" />生成新 Key</button>
              <div class="settings-api-endpoints">
                <span><em>POST</em><code>/api/v1/mailboxes/claim</code></span>
                <span><code>/email-code</code><a href="/email-code" target="_blank" rel="noopener"><ExternalLink :size="12" />打开页面</a></span>
              </div>
            </div>
            <p class="mt-2 text-[11px] leading-5 text-slate-400">生成或修改后点击“保存系统设置”立即生效；公共邮箱取码页面不使用这个 Key。</p>
          </div>
        </section>
        </div>
      </form>

      <section id="version-updates" class="panel settings-version-card scroll-mt-20 overflow-hidden">
        <div class="flex flex-col gap-4 border-b border-slate-100 p-5 dark:border-slate-700 sm:flex-row sm:items-start sm:justify-between sm:p-6">
          <div class="flex min-w-0 items-start gap-3">
            <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300"><RefreshCw :size="20" /></span>
            <div class="min-w-0">
              <h2 class="text-base font-black text-slate-900 dark:text-slate-100">版本与更新</h2>
              <p class="mt-1 text-xs leading-5 text-slate-400">根据仓库公告配置检查版本，无需 API Token；当前只提供查看。</p>
            </div>
          </div>
          <div class="flex shrink-0 flex-wrap gap-2">
            <a v-if="updateState.status?.repository_url" class="secondary-button" :href="updateState.status.repository_url" target="_blank" rel="noopener noreferrer"><FolderGit2 :size="16" />打开仓库</a>
            <button type="button" class="primary-button" :disabled="updateState.loading || updateState.status?.enabled === false" @click="checkForUpdates">
              <LoaderCircle v-if="showChecking" :size="17" class="animate-spin" />
              <RefreshCw v-else :size="17" />
              {{ showChecking ? '正在检查' : updateState.status?.enabled === false ? '检查更新已关闭' : '检查更新' }}
            </button>
          </div>
        </div>

        <div class="grid gap-px bg-slate-200 dark:bg-slate-700 sm:grid-cols-2 lg:grid-cols-4">
          <div class="min-h-[5.25rem] bg-white px-5 py-4 dark:bg-slate-800"><span class="flex h-4 items-center gap-1.5 text-[10px] font-bold uppercase leading-4 tracking-[0.14em] text-slate-400"><PackageOpen :size="12" />当前版本</span><strong class="mt-1 block h-5 truncate text-sm font-semibold leading-5 text-slate-800 dark:text-slate-100">{{ updateState.status?.current?.version || '2.1.2' }}</strong></div>
          <div class="min-h-[5.25rem] bg-white px-5 py-4 dark:bg-slate-800"><span class="flex h-4 items-center gap-1.5 text-[10px] font-bold uppercase leading-4 tracking-[0.14em] text-slate-400"><GitCommit :size="12" />构建提交</span><strong class="mt-1 block h-5 truncate text-sm font-semibold leading-5 text-slate-800 dark:text-slate-100">{{ shortCommit(updateState.status?.current?.commit) }}</strong></div>
          <div class="min-h-[5.25rem] bg-white px-5 py-4 dark:bg-slate-800"><span class="flex h-4 items-center gap-1.5 text-[10px] font-bold uppercase leading-4 tracking-[0.14em] text-slate-400"><Monitor :size="12" />运行平台</span><strong class="mt-1 block h-5 truncate text-sm font-semibold leading-5 text-slate-800 dark:text-slate-100">{{ updateState.status?.current ? `${updateState.status.current.os} / ${updateState.status.current.arch}` : '-' }}</strong></div>
          <div class="min-h-[5.25rem] bg-white px-5 py-4 dark:bg-slate-800"><span class="flex h-4 items-center gap-1.5 text-[10px] font-bold uppercase leading-4 tracking-[0.14em] text-slate-400"><CalendarClock :size="12" />检查时间</span><strong class="mt-1 block h-5 truncate text-sm font-semibold leading-5 text-slate-800 dark:text-slate-100">{{ formatDate(updateState.status?.checked_at) }}</strong></div>
        </div>

        <div class="p-5 sm:p-6">
          <div v-if="updateState.status?.error" class="flex items-start gap-3 rounded-xl border border-rose-200 bg-rose-50 p-4 dark:border-rose-900 dark:bg-rose-950/30">
            <CircleAlert :size="19" class="mt-0.5 shrink-0 text-rose-500" />
            <div><strong class="text-sm text-rose-700 dark:text-rose-300">检查更新失败</strong><p class="mt-1 break-words text-xs leading-5 text-rose-600/80 dark:text-rose-300/80">{{ updateState.status.error }}</p></div>
          </div>
          <div v-else-if="updateState.status?.latest" :class="updateState.status.update_available ? 'border-amber-200 bg-amber-50 dark:border-amber-900 dark:bg-amber-950/30' : 'border-emerald-200 bg-emerald-50 dark:border-emerald-900 dark:bg-emerald-950/30'" class="flex flex-col gap-4 rounded-xl border p-4 sm:flex-row sm:items-center sm:justify-between">
            <div class="flex min-w-0 items-start gap-3">
              <CircleAlert v-if="updateState.status.update_available" :size="19" class="mt-0.5 shrink-0 text-amber-500" />
              <CheckCircle2 v-else :size="19" class="mt-0.5 shrink-0 text-emerald-500" />
              <div class="min-w-0">
                <strong class="block text-sm text-slate-800 dark:text-slate-100">{{ updateState.status.update_available ? '发现新的项目内容' : '当前已经是最新版本' }}</strong>
                <p class="mt-1 truncate text-xs text-slate-500 dark:text-slate-300">{{ updateState.status.latest.name }}</p>
                <p v-if="updateState.status.latest.notes" class="mt-1 overflow-hidden text-ellipsis whitespace-nowrap text-[11px] text-slate-400">{{ updateState.status.latest.notes }}</p>
              </div>
            </div>
            <a v-if="updateState.status.latest.url" class="secondary-button shrink-0" :href="updateState.status.latest.url" target="_blank" rel="noopener noreferrer"><ExternalLink :size="16" />重新下载源码</a>
          </div>
          <div v-else class="rounded-xl border border-slate-200 bg-slate-50 p-4 text-xs leading-5 text-slate-400 dark:border-slate-700 dark:bg-slate-900/40">{{ updateState.status?.enabled === false ? '配置文件已关闭更新检查。' : '点击“检查更新”读取仓库公告配置。' }}</div>
        </div>
      </section>
    </template>
  </div>
</template>
