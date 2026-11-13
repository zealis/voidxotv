<script setup lang="ts">
// 消息中心（§9.1 / §9.4 / §9.5）：首期三分组——互动通知（赞 / 收藏 / 评论 / @）、
// 关注通知、系统通知（审核 / 申诉 / 举报 / 账号）。私信会话为演进预留，不在首期。
// 列表项 = 头像 / 图标、标题、摘要、时间、未读徽标；未读用「徽标 + 字重」表达，不只靠颜色。
// 删除 / 免打扰通过条目「⋯」菜单提供非手势入口（§9.1 左滑操作的非手势替代）；
// 免打扰写回设置页的分组开关（§9.3：配置位于设置页，消息页只提供快捷入口）。
// 私有页面：动态 SSR、禁止公共缓存、不参与索引。
import { ApiError, errorMessage } from '~/composables/useApi'

const { open: openAuthDialog } = useAuthDialog()

useSeoMeta({ title: '消息 · 夜街', robots: 'noindex,nofollow' })

type GroupKey = 'interaction' | 'follow' | 'system'

interface Notification {
  id: number
  kind: string
  category: string
  title: string
  body: string
  read_at?: string
  created_at: string
  actor_avatar_url?: string
}

interface GroupMeta {
  key: GroupKey
  label: string
  icon: string
  emptyTitle: string
  emptyHint: string
}

const GROUPS: GroupMeta[] = [
  {
    key: 'interaction', label: '互动', icon: 'heart',
    emptyTitle: '还没有互动通知',
    emptyHint: '作品被点赞、收藏或评论时会出现在这里。',
  },
  {
    key: 'follow', label: '关注', icon: 'user',
    emptyTitle: '还没有关注通知',
    emptyHint: '有人关注你时会在这里提醒。',
  },
  {
    key: 'system', label: '系统', icon: 'shield',
    emptyTitle: '还没有系统通知',
    emptyHint: '审核结论、申诉结果与账号状态变动会出现在这里。',
  },
]

const kindText: Record<string, string> = {
  interaction: '互动',
  follow: '关注',
  moderation: '审核',
  appeal: '申诉',
  report: '举报',
  account: '账号',
  system: '系统',
}

function groupMeta(category: string): GroupMeta {
  return GROUPS.find((g) => g.key === category) || GROUPS[2]!
}

const { api, getMe } = useApi()
const asyncState = useAsyncState({
  keepOnError: true,
  isEmpty: () => tabs[active.value].items.length === 0,
})
const { phase, errorText, partial, retrying } = asyncState

const active = ref<GroupKey>('interaction')
const checking = ref(true)
const needLogin = ref(false)
const loading = ref(false)
const message = ref('')

interface TabState {
  items: Notification[]
  cursor: string | number | null
  touched: boolean
}

const tabs = reactive<Record<GroupKey, TabState>>({
  interaction: { items: [], cursor: null, touched: false },
  follow: { items: [], cursor: null, touched: false },
  system: { items: [], cursor: null, touched: false },
})

// 未读分桶来自接口的 unread_by_category，三键恒齐；总数在头部展示。
const buckets = reactive<Record<GroupKey, number>>({ interaction: 0, follow: 0, system: 0 })
const unread = computed(() => buckets.interaction + buckets.follow + buckets.system)
const activeMeta = computed(() => groupMeta(active.value))

function applyBuckets(value: Record<string, number> | undefined) {
  if (!value) return
  buckets.interaction = Number(value.interaction) || 0
  buckets.follow = Number(value.follow) || 0
  buckets.system = Number(value.system) || 0
}

async function load(group: GroupKey, reset = true) {
  const target = tabs[group]
  if (!reset && target.cursor === null) return
  loading.value = true
  if (reset) {
    message.value = ''
    asyncState.begin()
  }
  try {
    const suffix = reset || target.cursor === null ? '' : `&cursor=${encodeURIComponent(String(target.cursor))}`
    const data = await api<{
      items: Notification[]
      next_cursor: string | number | null
      unread: number
      unread_by_category: Record<string, number>
    }>(`/api/v1/notifications?limit=20&category=${group}${suffix}`)
    target.items = reset ? data.items : [...target.items, ...data.items]
    target.cursor = data.next_cursor
    target.touched = true
    applyBuckets(data.unread_by_category)
    if (reset) asyncState.succeed()
  } catch (error) {
    if (reset) asyncState.fail(error)
    message.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

function openGroup(group: GroupKey) {
  if (active.value === group) return
  active.value = group
  // 只在首次进入分组时拉取；来回切换保留各自的列表与分页进度。
  if (!tabs[group].touched) load(group)
}

// 点条目即标记已读；已读项不重复请求。
async function markOne(item: Notification) {
  if (item.read_at) return
  try {
    const data = await api<{ unread_by_category: Record<string, number> }>(
      '/api/v1/notifications/read', { method: 'POST', body: { ids: [item.id] } },
    )
    item.read_at = new Date().toISOString()
    applyBuckets(data.unread_by_category)
  } catch (error) {
    message.value = errorMessage(error)
  }
}

// 「本组已读」只清当前分组（§9.1 分组内操作），其他分组未读不受影响。
async function markGroupRead() {
  const group = active.value
  try {
    const data = await api<{ unread_by_category: Record<string, number> }>(
      '/api/v1/notifications/read', { method: 'POST', body: { category: group } },
    )
    tabs[group].items = tabs[group].items.map(
      (item) => ({ ...item, read_at: item.read_at || new Date().toISOString() }),
    )
    applyBuckets(data.unread_by_category)
  } catch (error) {
    message.value = errorMessage(error)
  }
}

// ── 条目菜单（删除 / 免打扰的非手势入口）────────────────────────

const menu = reactive<{ open: boolean; item: Notification | null; mode: 'actions' | 'mute' }>({
  open: false, item: null, mode: 'actions',
})
const busy = ref(false)

function openMenu(item: Notification) {
  menu.item = item
  menu.mode = 'actions'
  menu.open = true
}

async function removeItem() {
  const item = menu.item
  if (!item || busy.value) return
  busy.value = true
  try {
    const data = await api<{ unread_by_category: Record<string, number> }>(
      `/api/v1/notifications/${item.id}`, { method: 'DELETE' },
    )
    for (const group of GROUPS) {
      tabs[group.key].items = tabs[group.key].items.filter((it) => it.id !== item.id)
    }
    applyBuckets(data.unread_by_category)
    message.value = '已删除该通知'
    menu.open = false
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

// 免打扰 = 关闭该分组的接收开关（与设置页同一配置，§9.3）；已收到的通知保留。
async function muteGroup() {
  const item = menu.item
  if (!item || busy.value) return
  busy.value = true
  try {
    const patch: Record<string, boolean> = { [`notify_${item.category}`]: false }
    await api('/api/v1/me/settings', { method: 'PATCH', body: patch })
    message.value = `已关闭「${groupMeta(item.category).label}通知」，可在设置 → 通知里重新开启`
    menu.open = false
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
    return
  }
  await load('interaction')
})

function when(value: string): string {
  const diff = Date.now() - new Date(value).getTime()
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时前`
  return `${Math.floor(minutes / 1440)} 天前`
}
</script>

<template>
  <main class="inbox" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/" class="back" aria-label="回到沉浸流">←</NuxtLink>
      <h1>消息</h1>
      <span v-if="unread" class="unread">{{ unread }} 条未读</span>
      <button v-if="buckets[active]" type="button" class="mark" @click="markGroupRead">本组已读</button>
    </header>

    <nav class="tabs" role="tablist" aria-label="通知分组">
      <button
        v-for="group in GROUPS" :key="group.key" type="button" role="tab"
        :aria-selected="active === group.key" :class="{ on: active === group.key }"
        @click="openGroup(group.key)"
      >
        {{ group.label }}
        <span v-if="buckets[group.key]" class="dot" :aria-label="`${buckets[group.key]} 条未读`">
          {{ buckets[group.key] > 99 ? '99+' : buckets[group.key] }}
        </span>
      </button>
    </nav>

    <p v-if="message" class="notice" role="status">{{ message }}</p>
    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后查看审核结论、申诉进展与举报反馈。</p>
      <button type="button" class="cta" @click="openAuthDialog()">登录</button>
    </section>

    <VpAsyncState
      v-else :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="5"
      :empty-title="activeMeta.emptyTitle" :empty-hint="activeMeta.emptyHint"
      @retry="load(active)"
    >
      <ul v-if="tabs[active].items.length" class="list">
        <li v-for="item in tabs[active].items" :key="item.id" :class="{ unread: !item.read_at }">
          <span class="avatar">
            <img v-if="item.actor_avatar_url" :src="item.actor_avatar_url" alt="" />
            <ImIcon v-else :name="groupMeta(item.category).icon" :size="20" />
          </span>
          <div class="main" @click="markOne(item)">
            <div class="head">
              <strong>{{ item.title }}</strong>
              <span v-if="!item.read_at" class="badge" aria-label="未读"></span>
              <time :datetime="item.created_at">{{ when(item.created_at) }}</time>
              <!-- 操作钮与时间同一条 flex 行：stop 避免冒泡成「标记已读」。 -->
              <button type="button" class="ops" aria-label="通知操作" @click.stop="openMenu(item)">
                <ImIcon name="more" :size="18" />
              </button>
            </div>
            <p class="body">{{ item.body }}</p>
            <span class="kind">{{ kindText[item.kind] || item.kind }}</span>
          </div>
        </li>
      </ul>
      <button
        v-if="tabs[active].cursor !== null" type="button" class="more"
        :disabled="loading" @click="load(active, false)"
      >{{ loading ? '加载中…' : '加载更多' }}</button>
    </VpAsyncState>

    <VpDialog
      :open="menu.open" :title="menu.mode === 'mute' ? '免打扰此分组' : '通知操作'"
      :busy="busy" variant="sheet" @close="menu.open = false"
    >
      <template v-if="menu.mode === 'actions' && menu.item">
        <p class="dialog-text">{{ menu.item.title }}</p>
        <div class="dialog-actions column">
          <button
            v-if="!menu.item.read_at" type="button" class="btn"
            @click="markOne(menu.item); menu.open = false"
          >标为已读</button>
          <button type="button" class="btn" @click="menu.mode = 'mute'">免打扰此类通知…</button>
          <button type="button" class="btn danger" :disabled="busy" @click="removeItem">
            {{ busy ? '删除中…' : '删除通知' }}
          </button>
          <button type="button" class="btn ghost" @click="menu.open = false">取消</button>
        </div>
      </template>
      <template v-else-if="menu.item">
        <p class="dialog-text">
          将关闭「{{ groupMeta(menu.item.category).label }}通知」的接收开关，之后这一组通知不再写入消息中心；
          已收到的通知保留，可随时在 设置 → 通知 里重新开启。
        </p>
        <div class="dialog-actions">
          <button type="button" class="btn" @click="menu.mode = 'actions'">返回</button>
          <button type="button" class="btn primary" :disabled="busy" @click="muteGroup">
            {{ busy ? '处理中…' : '确认关闭' }}
          </button>
        </div>
      </template>
    </VpDialog>
  </main>
</template>

<style scoped>
.inbox {
  min-height: 100dvh;
  padding: 20px 20px 56px;
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.bar {
  display: flex; align-items: center; gap: 10px;
  max-width: 760px; margin: 0 auto 14px;
}
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.unread { font-size: 12px; color: var(--vp-im-accent); }
.mark {
  margin-left: auto; padding: 5px 12px; border-radius: 99px; cursor: pointer;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-primary); font-size: 12px;
}
.tabs {
  display: flex; gap: 8px; max-width: 760px; margin: 0 auto 16px;
}
.tabs button {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 7px 16px; border-radius: 99px; cursor: pointer; font-size: 13px;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-secondary);
}
.tabs button.on {
  background: var(--vp-im-accent); border-color: var(--vp-im-accent); color: #fff;
}
.tabs .dot {
  min-width: 18px; height: 18px; padding: 0 5px; border-radius: 99px;
  display: inline-grid; place-items: center;
  background: var(--vp-im-accent); color: #fff; font-size: 11px; font-weight: 600;
}
.tabs button.on .dot { background: rgba(255, 255, 255, 0.24); }
.list { list-style: none; margin: 0 auto; padding: 0; max-width: 760px; display: grid; gap: 10px; }
.list li {
  display: flex; align-items: flex-start; gap: 12px;
  padding: 14px 16px; border-radius: 14px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.06);
}
.list li.unread {
  border-color: rgba(255, 46, 126, 0.45); background: rgba(255, 46, 126, 0.07);
}
.avatar {
  flex: none; width: 40px; height: 40px; border-radius: 50%;
  display: grid; place-items: center; overflow: hidden;
  background: rgba(255, 255, 255, 0.08); color: var(--vp-im-text-secondary);
}
.avatar img { width: 100%; height: 100%; object-fit: cover; }
.main { flex: 1; min-width: 0; cursor: pointer; }
.head { display: flex; align-items: center; gap: 8px; }
.head strong { font-size: 13px; font-weight: 400; }
.list li.unread .head strong { font-weight: 600; }
.badge {
  flex: none; width: 8px; height: 8px; border-radius: 50%; background: var(--vp-im-accent);
}
.head time { margin-left: auto; flex: none; font-size: 11px; color: var(--vp-im-text-secondary); }
.body { margin: 5px 0 6px; font-size: 12.5px; line-height: 1.7; color: var(--vp-im-text-secondary); white-space: pre-wrap; }
.kind {
  display: inline-block; padding: 1px 8px; border-radius: 99px; font-size: 11px;
  background: rgba(255, 255, 255, 0.1); color: var(--vp-im-text-secondary);
}
/* 操作钮在标题行内与时间居中对齐：负外边距抵消按钮盒撑高标题行，行高节奏不变。
   必须重置按钮默认 padding（1px 6px）：border-box 24px 被 padding 挤成 12px 内容盒，
   18px 图标会溢出并顶到右 padding 缘（水平偏 3px）。 */
.ops {
  flex: none; width: 24px; height: 24px; margin-block: -5px;
  padding: 0;
  display: grid; place-items: center;
  border: none; background: transparent; border-radius: 50%; cursor: pointer;
  color: var(--vp-im-text-secondary);
}
.ops:hover, .ops:focus-visible { background: rgba(255, 255, 255, 0.08); color: var(--vp-im-text-primary); }
.gate { max-width: 760px; margin: 40px auto; text-align: center; }
.gate p { margin: 0 0 14px; font-size: 13px; color: var(--vp-im-text-secondary); }
.cta {
  border: 0; cursor: pointer; font-family: inherit;
  display: inline-block; padding: 8px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: #fff; font-size: 13px; text-decoration: none;
}
.notice { max-width: 760px; margin: 0 auto 12px; font-size: 12px; color: var(--vp-im-accent); }
.hint { max-width: 760px; margin: 24px auto; font-size: 13px; color: var(--vp-im-text-secondary); }
.more {
  display: block; margin: 22px auto 0; padding: 8px 22px; border-radius: 99px;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-primary); font-size: 13px; cursor: pointer;
}
.more:disabled { opacity: 0.5; cursor: not-allowed; }
.dialog-text { margin: 0 0 16px; font-size: 13px; line-height: 1.7; }
.dialog-actions { display: flex; gap: 10px; justify-content: flex-end; }
.dialog-actions.column { flex-direction: column; }
.dialog-actions .btn { width: 100%; }
.dialog-actions:not(.column) .btn { width: auto; }
.btn {
  padding: 9px 18px; border-radius: 10px; cursor: pointer; font-size: 13px;
  border: 1px solid var(--vp-tool-border, rgba(255, 255, 255, 0.16));
  background: transparent; color: inherit;
}
.btn.primary { background: var(--vp-im-accent); border-color: var(--vp-im-accent); color: #fff; }
.btn.danger { border-color: rgba(255, 69, 58, 0.6); color: #ff6b5e; }
.btn.ghost { border-color: transparent; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
