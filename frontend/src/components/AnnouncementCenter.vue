<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Bell, BellRing, CheckCheck, ExternalLink, GitCommit, Megaphone, PackageOpen, Sparkles, X } from '@lucide/vue'
import { useUpdates } from '../composables/useUpdates'

const {
  updateState,
  announcements,
  unreadAnnouncements,
  loadUpdates,
  markAnnouncementRead,
  markAllAnnouncementsRead,
} = useUpdates()

const root = ref(null)
const open = ref(false)
const activeAnnouncement = ref(null)
const unreadLabel = computed(() => unreadAnnouncements.value.length > 9 ? '9+' : String(unreadAnnouncements.value.length))

function typeMeta(type) {
  if (type === 'update') return { label: '版本更新', icon: PackageOpen, tone: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300' }
  if (type === 'system') return { label: '系统公告', icon: Sparkles, tone: 'bg-violet-100 text-violet-700 dark:bg-violet-950/60 dark:text-violet-300' }
  return { label: '项目公告', icon: Megaphone, tone: 'bg-sky-100 text-sky-700 dark:bg-sky-950/60 dark:text-sky-300' }
}

function formatDate(value) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
}

function isUnread(item) {
  return unreadAnnouncements.value.some((announcement) => announcement.id === item.id)
}

function toggleCenter() {
  open.value = !open.value
}

function showAnnouncement(item) {
  markAnnouncementRead(item.id)
  activeAnnouncement.value = item
  open.value = false
}

function closeDetails() {
  activeAnnouncement.value = null
}

function handleDocumentClick(event) {
  if (open.value && root.value && !root.value.contains(event.target)) open.value = false
}

function handleKeydown(event) {
  if (event.key !== 'Escape') return
  if (activeAnnouncement.value) closeDetails()
  else open.value = false
}

onMounted(() => {
  document.addEventListener('click', handleDocumentClick)
  window.addEventListener('keydown', handleKeydown)
  loadUpdates().catch(() => {})
})
onBeforeUnmount(() => {
  document.removeEventListener('click', handleDocumentClick)
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <div ref="root" class="relative">
    <button class="relative flex h-9 w-9 items-center justify-center rounded-xl text-slate-400 transition hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-800 dark:hover:text-slate-200" :title="unreadAnnouncements.length ? `${unreadAnnouncements.length} 条未读公告` : '公告中心'" aria-label="打开公告中心" :aria-expanded="open" @click.stop="toggleCenter">
      <BellRing v-if="unreadAnnouncements.length" :size="18" />
      <Bell v-else :size="18" />
      <span v-if="unreadAnnouncements.length" class="absolute -right-0.5 -top-0.5 flex min-h-4 min-w-4 items-center justify-center rounded-full border-2 border-white bg-rose-500 px-0.5 text-[9px] font-black leading-none text-white dark:border-slate-900">{{ unreadLabel }}</span>
    </button>

    <Transition name="dropdown">
      <section v-if="open" class="absolute right-0 top-12 z-40 w-[min(23rem,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl dark:border-slate-700 dark:bg-slate-800" aria-label="公告中心" @click.stop>
        <header class="flex h-14 items-center justify-between border-b border-slate-100 px-4 dark:border-slate-700">
          <div>
            <h2 class="text-sm font-black text-slate-900 dark:text-slate-100">公告中心</h2>
            <p class="mt-0.5 text-[10px] text-slate-400">版本更新与项目消息</p>
          </div>
          <button v-if="unreadAnnouncements.length" type="button" class="inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-[11px] font-bold text-emerald-600 transition hover:bg-emerald-50 dark:text-emerald-300 dark:hover:bg-emerald-950/40" @click="markAllAnnouncementsRead"><CheckCheck :size="14" />全部已读</button>
        </header>

        <div v-if="announcements.length" class="max-h-[22.5rem] overflow-y-auto">
          <button v-for="item in announcements" :key="item.id" type="button" class="group flex min-h-[4.5rem] w-full gap-3 border-b border-slate-100 px-4 py-3 text-left transition last:border-b-0 hover:bg-slate-50 dark:border-slate-700/70 dark:hover:bg-slate-700/50" @click="showAnnouncement(item)">
            <span :class="typeMeta(item.type).tone" class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg"><component :is="typeMeta(item.type).icon" :size="16" /></span>
            <span class="min-w-0 flex-1">
              <span class="flex items-center gap-2"><strong class="min-w-0 flex-1 truncate text-xs text-slate-800 dark:text-slate-100">{{ item.title }}</strong><span v-if="isUnread(item)" class="h-2 w-2 shrink-0 rounded-full bg-rose-500" /></span>
              <span class="mt-1 block overflow-hidden text-ellipsis whitespace-nowrap text-[11px] text-slate-400">{{ item.summary || item.content }}</span>
              <span class="mt-1 flex items-center justify-between gap-2 text-[10px] text-slate-400"><span>{{ typeMeta(item.type).label }}</span><time>{{ formatDate(item.published_at) }}</time></span>
            </span>
          </button>
        </div>
        <div v-else class="flex min-h-40 flex-col items-center justify-center px-5 text-center">
          <Megaphone :size="26" class="text-slate-300 dark:text-slate-600" />
          <p class="mt-3 text-sm font-bold text-slate-500 dark:text-slate-300">暂无公告</p>
          <p class="mt-1 text-xs text-slate-400">版本更新和项目消息会显示在这里。</p>
        </div>
      </section>
    </Transition>
  </div>

  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="activeAnnouncement" class="fixed inset-0 z-[110] flex items-center justify-center bg-slate-950/45 p-4 backdrop-blur-[2px]" role="presentation" @click.stop>
        <article class="flex max-h-[min(42rem,calc(100vh-2rem))] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl dark:border-slate-700 dark:bg-slate-800" role="dialog" aria-modal="true" aria-labelledby="announcement-dialog-title">
          <header class="flex items-start gap-3 border-b border-slate-100 p-5 dark:border-slate-700">
            <span :class="typeMeta(activeAnnouncement.type).tone" class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl"><component :is="typeMeta(activeAnnouncement.type).icon" :size="20" /></span>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 text-[10px] font-bold text-slate-400"><span>{{ typeMeta(activeAnnouncement.type).label }}</span><span>·</span><time>{{ formatDate(activeAnnouncement.published_at) }}</time></div>
              <h2 id="announcement-dialog-title" class="mt-1 text-base font-black leading-6 text-slate-900 dark:text-slate-100">{{ activeAnnouncement.title }}</h2>
            </div>
            <button type="button" class="icon-button -mr-1 -mt-1" title="关闭公告" @click="closeDetails"><X :size="18" /></button>
          </header>
          <div class="min-h-0 flex-1 overflow-y-auto p-5">
            <p class="whitespace-pre-line break-words text-sm leading-7 text-slate-600 dark:text-slate-300">{{ activeAnnouncement.content || activeAnnouncement.summary }}</p>
          </div>
          <footer v-if="activeAnnouncement.url" class="flex justify-end border-t border-slate-100 px-5 py-4 dark:border-slate-700">
            <a class="secondary-button" :href="activeAnnouncement.url" target="_blank" rel="noopener noreferrer"><ExternalLink :size="16" />查看相关页面</a>
          </footer>
        </article>
      </div>
    </Transition>
  </Teleport>
</template>
