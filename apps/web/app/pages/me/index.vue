<script setup lang="ts">
// 「我」：个人主页（作品 / 喜欢 / 收藏 / 观看历史 / 晚点再看）卡片网格。
// 私有页面：动态 SSR、禁止公共缓存、不参与索引。
import { ApiError, errorMessage } from '~/composables/useApi'

const { open: openAuthDialog } = useAuthDialog()

useSeoMeta({ title: '我 · 夜街', robots: 'noindex,nofollow' })

interface ContentItem {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  description: string
  publish_state: string
  review_status: string
  media_status?: string
  visibility: string
  created_at: string
  poster_url?: string
  like_count?: number
}

interface ReactedItem extends ContentItem {
  author_handle: string
}

interface LibraryItem {
  content_id: number
  slug: string
  type: 'video' | 'image'
  title: string
  author_handle: string
  poster_url?: string
  position_ms?: number
  duration_ms?: number
}

interface AppealItem {
  id: number
  content_id: number
  status: 'pending' | 'accepted' | 'rejected'
  decision_note?: string
}

interface MyProfile {
  handle: string
  display_name: string
  bio: string
  avatar_url?: string
  /** 自己是已认证创作者时，昵称旁挂标签。 */
  verified_creator?: boolean
}

type ProfileTab = 'works' | 'likes' | 'favorites' | 'history' | 'watchlist'

interface Card {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  poster?: string
  likes?: number
  author?: string
  href: string
  badge?: string
  badgeTone?: 'warn' | 'ok' | 'muted'
  progress?: number
  appealable?: boolean
  appealLabel?: string
  raw?: ContentItem
}

const { api, getMe } = useApi()
const asyncState = useAsyncState({ keepOnError: true })
const { phase, errorText, partial, retrying } = asyncState

const tabs: { key: ProfileTab; label: string; private: boolean }[] = [
  { key: 'works', label: '作品', private: false },
  { key: 'likes', label: '喜欢', private: true },
  { key: 'favorites', label: '收藏', private: true },
  { key: 'history', label: '观看历史', private: true },
  { key: 'watchlist', label: '晚点再看', private: true },
]

const items = ref<ContentItem[]>([])
const tab = ref<ProfileTab>('works')
const counts = ref<{ works: number; likes_received: number; following: number; followers: number } | null>(null)
const profile = ref<MyProfile | null>(null)
const lists = reactive<Record<ProfileTab, Card[]>>({
  works: [], likes: [], favorites: [], history: [], watchlist: [],
})
const loaded = reactive<Record<ProfileTab, boolean>>({
  works: false, likes: false, favorites: false, history: false, watchlist: false,
})
const reactedLoading = ref(false)
const appeals = ref<Record<number, AppealItem>>({})
const loading = ref(false)
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')
const query = ref('')
const manageMode = ref(false)
const selected = ref<number[]>([])
const deleteOpen = ref(false)
const deleting = ref(false)
const dialog = reactive({
  open: false,
  item: null as ContentItem | null,
  reason: '',
  pending: false,
  error: '',
})

const statusText: Record<string, string> = {
  pending: '待审核', approved: '已通过', rejected: '已驳回',
  taken_down: '已下架', limited: '已限流',
}
const mediaText: Record<string, string> = {
  processing: '处理中', failed: '处理失败', none: '处理失败',
}
const appealable = new Set(['rejected', 'taken_down', 'limited'])

async function load() {
  loading.value = true
  message.value = ''
  asyncState.begin()
  try {
    const [works, mine, stats, owner] = await Promise.all([
      api<{ items: ContentItem[] }>('/api/v1/contents/mine?limit=50'),
      api<{ items: AppealItem[] }>('/api/v1/appeals/mine'),
      api<{ works: number; likes_received: number; following: number; followers: number }>(
        '/api/v1/me/counts').catch(() => null),
      api<MyProfile>('/api/v1/me/profile').catch(() => null),
    ])
    counts.value = stats
    profile.value = owner
    items.value = works.items || []
    appeals.value = Object.fromEntries((mine.items || []).map((item) => [item.content_id, item]))
    asyncState.succeed()
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
      asyncState.succeed()
    } else {
      asyncState.fail(error)
    }
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
    return
  }
  await load()
})

function workCard(item: ContentItem): Card {
  let badge = ''
  let badgeTone: Card['badgeTone'] = 'muted'
  if (item.publish_state === 'draft') {
    badge = '草稿'
  } else if (item.publish_state === 'scheduled') {
    badge = '定时发布'
  } else if (item.review_status === 'pending') {
    badge = '待审核'
    badgeTone = 'warn'
  } else if (appealable.has(item.review_status)) {
    badge = statusText[item.review_status] || item.review_status
    badgeTone = 'warn'
  } else if (item.type === 'video' && item.publish_state !== 'draft' && mediaText[item.media_status || '']) {
    badge = mediaText[item.media_status || ''] || ''
    badgeTone = 'warn'
  } else if (item.review_status === 'approved') {
    badge = '已通过'
    badgeTone = 'ok'
  }
  const record = appeals.value[item.id]
  let appealLabel = ''
  if (record) {
    appealLabel = record.status === 'pending' ? '申诉处理中'
      : record.status === 'accepted' ? '申诉已通过' : '申诉未通过'
  }
  return {
    id: item.id,
    slug: item.slug,
    type: item.type,
    title: item.title || '（无标题）',
    poster: item.poster_url,
    likes: item.like_count || 0,
    href: item.publish_state === 'draft'
      ? `/publish?draft=${item.id}`
      : `/${item.type}/${item.slug}`,
    badge,
    badgeTone,
    appealable: appealable.has(item.review_status) && record?.status !== 'pending',
    appealLabel,
    raw: item,
  }
}

// 作品卡片带审核/申诉状态，状态可能随申诉提交变化，所以每次从 items 重算。
const workCards = computed<Card[]>(() => items.value.map(workCard))

const cards = computed<Card[]>(() => {
  const base = tab.value === 'works' ? workCards.value : lists[tab.value]
  const q = query.value.trim().toLowerCase()
  if (!q) return base
  return base.filter((card) =>
    card.title.toLowerCase().includes(q) || (card.author || '').toLowerCase().includes(q))
})

const searchPlaceholder = computed(() => ({
  works: '搜索我的作品', likes: '搜索你赞过的作品', favorites: '搜索你收藏的作品',
  history: '搜索观看历史', watchlist: '搜索晚点再看',
})[tab.value])

const emptyText = computed(() => ({
  works: '还没有作品，去发布第一条吧。',
  likes: '还没有喜欢的作品，去沉浸流里点点看。',
  favorites: '还没有收藏的作品，看到喜欢的点右侧收藏。',
  history: '还没有观看记录，去看几条视频吧。',
  watchlist: '晚点再看还是空的。',
})[tab.value])

function fromLibrary(item: LibraryItem): Card {
  const duration = item.duration_ms || 0
  const position = item.position_ms || 0
  return {
    id: item.content_id,
    slug: item.slug,
    type: item.type,
    title: item.title || '（无标题）',
    poster: item.poster_url,
    author: item.author_handle,
    href: `/${item.type}/${item.slug}`,
    progress: duration > 0 ? Math.min(100, Math.round((position / duration) * 100)) : undefined,
  }
}

function fromReacted(item: ReactedItem): Card {
  return {
    id: item.id,
    slug: item.slug,
    type: item.type,
    title: item.title || '（无标题）',
    poster: item.poster_url,
    likes: item.like_count || 0,
    author: item.author_handle,
    href: `/${item.type}/${item.slug}`,
  }
}

// 非作品分区按需加载，切回来不重复请求。
async function switchTab(next: ProfileTab) {
  tab.value = next
  query.value = ''
  manageMode.value = false
  selected.value = []
  if (loaded[next] || next === 'works') return
  reactedLoading.value = true
  try {
    if (next === 'history' || next === 'watchlist') {
      const data = await api<{ items: LibraryItem[] }>(`/api/v1/me/${next}?limit=50`)
      lists[next] = (data.items || []).map(fromLibrary)
    } else {
      const data = await api<{ items: ReactedItem[] }>(`/api/v1/me/${next}?limit=50`)
      lists[next] = (data.items || []).map(fromReacted)
    }
    loaded[next] = true
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    reactedLoading.value = false
  }
}

function formatCount(value?: number): string {
  const n = value || 0
  if (n >= 10000) return `${(n / 10000).toFixed(1).replace(/\.0$/, '')}万`
  return String(n)
}

function toggleSelect(id: number) {
  const index = selected.value.indexOf(id)
  if (index >= 0) selected.value.splice(index, 1)
  else selected.value.push(id)
}

function exitManage() {
  manageMode.value = false
  selected.value = []
}

async function deleteSelected() {
  if (!selected.value.length) return
  deleting.value = true
  try {
    await api('/api/v1/contents/batch-delete', {
      method: 'POST', body: { ids: [...selected.value] },
    })
    deleteOpen.value = false
    exitManage()
    await load()
  } catch (error) {
    message.value = errorMessage(error)
    deleteOpen.value = false
  } finally {
    deleting.value = false
  }
}

function openAppeal(item: ContentItem) {
  dialog.open = true
  dialog.item = item
  dialog.reason = ''
  dialog.error = ''
}

async function submitAppeal() {
  if (!dialog.item) return
  const reason = dialog.reason.trim()
  if (reason.length < 5) {
    dialog.error = '请填写至少 5 个字符的申诉理由'
    return
  }
  dialog.pending = true
  dialog.error = ''
  try {
    await api(`/api/v1/contents/${dialog.item.id}/appeal`, {
      method: 'POST', body: { reason },
    })
    dialog.open = false
    await load()
  } catch (error) {
    dialog.error = errorMessage(error)
  } finally {
    dialog.pending = false
  }
}
</script>

<template>
  <main class="me" data-surface="immersive">
    <p v-if="message" class="notice" role="status">{{ message }}</p>
    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后管理自己的作品、查看审核结论与申诉进度。</p>
      <button type="button" class="cta" @click="openAuthDialog()">登录</button>
    </section>

    <VpAsyncState
      v-else :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="cards" :rows="2" @retry="load()"
    >
      <!-- 顶部资料区：渐变横幅 + 头像 + 计数 + 账号标识 -->
      <header v-if="profile" class="hero" aria-label="本人资料">
        <div class="hero-top">
          <NuxtLink to="/" class="back" aria-label="回到首页">←</NuxtLink>
          <nav class="quick" aria-label="常用入口">
            <NuxtLink to="/studio">创作中心</NuxtLink>
            <NuxtLink to="/me/drafts">草稿箱</NuxtLink>
            <NuxtLink to="/library">个人库</NuxtLink>
            <NuxtLink to="/messages">消息</NuxtLink>
            <NuxtLink to="/settings">设置</NuxtLink>
            <NuxtLink to="/publish" class="publish">发布作品</NuxtLink>
          </nav>
        </div>

        <div class="hero-body">
          <span class="avatar">
            <img
              v-if="profile.avatar_url"
              :src="profile.avatar_url" :alt="`${profile.display_name || profile.handle} 的头像`"
            />
            <span v-else>{{ (profile.display_name || profile.handle).slice(0, 1) }}</span>
          </span>

          <div class="who">
            <div class="name">
              <strong>
                {{ profile.display_name || profile.handle }}
                <CreatorBadge v-if="profile.verified_creator" size="16" />
              </strong>
              <NuxtLink to="/settings" class="edit" aria-label="编辑资料" title="编辑资料">
                <ImIcon name="edit" :size="14" />
              </NuxtLink>
            </div>

            <div class="counts" aria-label="账号数据">
              <NuxtLink to="/me/relations?tab=following">
                <span>关注</span><strong>{{ counts?.following ?? 0 }}</strong>
              </NuxtLink>
              <NuxtLink to="/me/relations?tab=followers">
                <span>粉丝</span><strong>{{ counts?.followers ?? 0 }}</strong>
              </NuxtLink>
              <NuxtLink to="/me/relations?tab=likes">
                <span>获赞</span><strong>{{ counts?.likes_received ?? 0 }}</strong>
              </NuxtLink>
            </div>

            <p class="handle">夜街号：{{ profile.handle }}</p>
            <p v-if="profile.bio" class="bio">{{ profile.bio }}</p>

            <div class="chips">
              <NuxtLink to="/settings">编辑资料</NuxtLink>
              <NuxtLink to="/me/drafts">草稿箱</NuxtLink>
            </div>
          </div>
        </div>
      </header>

      <!-- 分区标签 -->
      <div class="tabbar">
        <nav class="tabs" aria-label="作品分区">
          <button
            v-for="entry in tabs" :key="entry.key" type="button"
            :class="{ on: tab === entry.key }"
            :aria-current="tab === entry.key ? 'page' : undefined"
            @click="switchTab(entry.key)"
          >
            {{ entry.label }}
            <small v-if="entry.key === 'works'">{{ counts?.works ?? items.length }}</small>
            <ImIcon v-if="entry.private" name="lock" :size="12" />
          </button>
        </nav>
        <button
          v-if="tab === 'works'" type="button" class="manage" @click="manageMode ? exitManage() : (manageMode = true)"
        >{{ manageMode ? '完成' : '批量管理' }}</button>
      </div>

      <div v-if="manageMode" class="manage-bar" role="status">
        <span>已选 {{ selected.length }} 项</span>
        <button type="button" class="danger" :disabled="!selected.length" @click="deleteOpen = true">删除所选</button>
        <button type="button" class="ghost" @click="exitManage">取消</button>
      </div>

      <div class="search-row">
        <label class="search">
          <ImIcon name="search" :size="14" />
          <input v-model="query" type="search" :placeholder="searchPlaceholder" :aria-label="searchPlaceholder" />
        </label>
      </div>

      <div v-if="reactedLoading" class="hint">加载中…</div>
      <div v-else-if="cards.length" class="grid">
        <article
          v-for="card in cards" :key="`${tab}-${card.id}`" class="card"
          :class="{ pickable: manageMode, picked: selected.includes(card.id) }"
          @click="manageMode && toggleSelect(card.id)"
        >
          <NuxtLink class="cover" :to="card.href" :aria-label="card.title">
            <img v-if="card.poster" :src="card.poster" alt="" loading="lazy" />
            <span v-else class="cover-fallback" :class="card.type">
              <ImIcon :name="card.type === 'image' ? 'image' : 'play'" :size="26" />
              <em>{{ card.type === 'image' ? '图集' : '视频' }}</em>
            </span>
            <span v-if="card.badge" class="badge" :class="card.badgeTone">{{ card.badge }}</span>
            <span v-if="card.progress !== undefined" class="watch"><i :style="{ width: `${card.progress}%` }" /></span>
            <span v-else class="likes">
              <ImIcon name="heart" :size="13" filled /> {{ formatCount(card.likes) }}
            </span>
            <span v-if="manageMode" class="pick" @click.stop>
              <input
                type="checkbox" :checked="selected.includes(card.id)"
                :aria-label="`选择《${card.title}》`" @change="toggleSelect(card.id)"
              />
            </span>
          </NuxtLink>
          <NuxtLink class="caption" :to="card.href" :title="card.title">{{ card.title }}</NuxtLink>
          <span v-if="card.author" class="author">@{{ card.author }}</span>
          <span v-if="card.appealLabel" class="appeal-state">{{ card.appealLabel }}</span>
          <button
            v-if="card.appealable && card.raw" type="button" class="appeal-mini"
            @click="openAppeal(card.raw)"
          >申诉</button>
        </article>
      </div>
      <p v-else class="empty">{{ query ? '没有匹配的作品。' : emptyText }}</p>
    </VpAsyncState>

    <VpDialog :open="deleteOpen" title="删除所选作品" :busy="deleting" @close="deleteOpen = false">
      <p class="tip">将删除选中的 {{ selected.length }} 条作品，此操作不可撤销。</p>
      <div class="actions">
        <button type="button" class="ghost" @click="deleteOpen = false">取消</button>
        <button type="button" class="primary-btn danger-btn" :disabled="deleting" @click="deleteSelected">
          {{ deleting ? '删除中…' : '确认删除' }}
        </button>
      </div>
    </VpDialog>

    <VpDialog :open="dialog.open" title="提交申诉" :busy="dialog.pending" @close="dialog.open = false">
      <p class="tip">
        作品《{{ dialog.item?.title || '（无标题）' }}》当前为
        {{ statusText[dialog.item?.review_status || ''] || dialog.item?.review_status }}。
        请说明你认为处理有误的具体原因（5–500 字），审核员会重新核对。
      </p>
      <textarea v-model="dialog.reason" rows="5" maxlength="500" placeholder="申诉理由" />
      <p v-if="dialog.error" class="error">{{ dialog.error }}</p>
      <div class="actions">
        <button type="button" class="ghost" @click="dialog.open = false">取消</button>
        <button type="button" class="primary-btn" :disabled="dialog.pending" @click="submitAppeal">
          {{ dialog.pending ? '提交中…' : '提交申诉' }}
        </button>
      </div>
    </VpDialog>

    <BottomNav />
  </main>
</template>

<style scoped>
.me {
  min-height: 100dvh;
  padding-bottom: calc(84px + env(safe-area-inset-bottom));
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.hero {
  position: relative;
  padding: 16px 28px 22px;
  overflow: hidden;
  background:
    radial-gradient(560px 220px at 78% -10%, rgba(124, 92, 255, 0.36), transparent 70%),
    linear-gradient(112deg, #12142d 0%, #1b1740 38%, #231a4d 62%, #111221 100%);
  border-bottom: 1px solid rgba(255, 255, 255, 0.07);
}
.hero::after {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: linear-gradient(118deg, transparent 46%, rgba(255, 255, 255, 0.045) 58%, transparent 70%);
}
.hero-top, .hero-body { position: relative; z-index: 1; max-width: 1560px; margin: 0 auto; }
.hero-top { display: flex; align-items: center; gap: 12px; margin-bottom: 26px; }
/* 与搜索 / 消息 / 个人库等页面同一套圆形边框返回钮，不再用纯文字链接。 */
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.quick { margin-left: auto; display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.quick a {
  padding: 5px 12px; border-radius: 99px; font-size: 12px; text-decoration: none;
  color: rgba(255, 255, 255, 0.78); background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.1); white-space: nowrap;
}
.quick a:hover { background: rgba(255, 255, 255, 0.16); color: #fff; }
.quick a.publish {
  background: var(--vp-im-accent); border-color: var(--vp-im-accent);
  color: #fff; font-weight: 600;
}
.hero-body { display: flex; align-items: flex-start; gap: 22px; }
.avatar {
  width: 104px; height: 104px; flex: none; border-radius: 50%; overflow: hidden;
  display: grid; place-items: center;
  background: linear-gradient(145deg, #8e9bd8, #6b6fa8);
  border: 2px solid rgba(255, 255, 255, 0.28);
  font-size: 34px; color: #fff;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.28);
}
.avatar img { width: 100%; height: 100%; object-fit: cover; display: block; }
.who { min-width: 0; flex: 1; display: grid; gap: 8px; }
.name { display: flex; align-items: center; gap: 8px; }
.name strong {
  display: inline-flex; align-items: center; gap: 6px;
  font-size: 20px; font-weight: 600; letter-spacing: 0.01em;
}
.edit {
  display: grid; place-items: center; width: 22px; height: 22px;
  color: rgba(255, 255, 255, 0.65); border-radius: 6px;
}
.edit:hover { color: #fff; background: rgba(255, 255, 255, 0.12); }
.counts { display: flex; align-items: baseline; gap: 22px; }
.counts a {
  display: inline-flex; align-items: baseline; gap: 5px;
  color: rgba(255, 255, 255, 0.72); text-decoration: none; font-size: 12px;
}
.counts a:hover { color: #fff; }
.counts strong { font-size: 14px; font-weight: 600; color: #fff; }
.handle { margin: 0; font-size: 12px; color: rgba(255, 255, 255, 0.6); }
.bio {
  margin: 0; font-size: 12px; line-height: 1.6;
  color: rgba(255, 255, 255, 0.86); overflow-wrap: anywhere;
}
.chips { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 2px; }
.chips a {
  padding: 6px 14px; border-radius: 8px; font-size: 12px; text-decoration: none;
  color: rgba(255, 255, 255, 0.86);
  background: rgba(255, 255, 255, 0.1); border: 1px solid rgba(255, 255, 255, 0.12);
}
.chips a:hover { background: rgba(255, 255, 255, 0.18); color: #fff; }

.tabbar {
  max-width: 1560px; margin: 0 auto; padding: 0 28px;
  display: flex; align-items: center; gap: 16px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.07);
}
.tabs { display: flex; align-items: center; gap: 4px; flex: 1; min-width: 0; overflow-x: auto; scrollbar-width: none; }
.tabs::-webkit-scrollbar { display: none; }
.tabs button {
  position: relative; flex: none; display: inline-flex; align-items: center; gap: 5px;
  padding: 14px 12px 13px; border: 0; background: transparent; cursor: pointer;
  color: var(--vp-im-text-secondary); font-size: 14px; white-space: nowrap;
}
.tabs button small { font-size: 12px; opacity: 0.75; }
.tabs button:hover { color: var(--vp-im-text-primary); }
.tabs button.on { color: var(--vp-im-text-primary); font-weight: 600; }
.tabs button.on::after {
  content: ''; position: absolute; left: 12px; right: 12px; bottom: 0;
  height: 3px; border-radius: 3px 3px 0 0; background: var(--vp-im-accent);
}
.manage {
  flex: none; padding: 6px 14px; border-radius: 7px; cursor: pointer; font-size: 12px;
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.06); color: var(--vp-im-text-primary);
}
.manage:hover { background: rgba(255, 255, 255, 0.12); }
.manage-bar {
  max-width: 1560px; margin: 10px auto 0; padding: 8px 14px;
  display: flex; align-items: center; gap: 12px;
  border-radius: 10px; background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
  font-size: 12px; color: var(--vp-im-text-secondary);
}
.manage-bar button { padding: 5px 14px; border-radius: 7px; cursor: pointer; font-size: 12px; }
.manage-bar .danger { border: 1px solid rgba(255, 105, 97, 0.55); background: rgba(255, 105, 97, 0.14); color: #ffb4ae; }
.manage-bar .danger:disabled { opacity: 0.45; cursor: not-allowed; }
.manage-bar .ghost { border: 1px solid rgba(255, 255, 255, 0.14); background: transparent; color: var(--vp-im-text-primary); }
.search-row { max-width: 1560px; margin: 0 auto; padding: 16px 28px 12px; display: flex; justify-content: flex-end; }
.search {
  display: inline-flex; align-items: center; gap: 8px; width: 240px; max-width: 100%;
  padding: 7px 12px; border-radius: 8px;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.09);
  color: var(--vp-im-text-secondary);
}
.search:focus-within { border-color: rgba(255, 255, 255, 0.26); }
.search input {
  flex: 1; min-width: 0; border: 0; background: transparent; outline: none;
  color: var(--vp-im-text-primary); font-size: 12px;
}
.search input:focus-visible { box-shadow: none; }
.search input::placeholder { color: rgba(255, 255, 255, 0.4); }

.grid {
  max-width: 1560px; margin: 0 auto; padding: 0 28px;
  display: grid; gap: 18px 14px;
  grid-template-columns: repeat(6, minmax(0, 1fr));
}
.card { min-width: 0; display: grid; gap: 5px; }
.cover {
  position: relative; display: block; aspect-ratio: 3 / 4; overflow: hidden;
  border-radius: 6px; background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.06);
}
.cover img { width: 100%; height: 100%; object-fit: cover; display: block; }
.cover-fallback {
  position: absolute; inset: 0; display: grid; place-content: center; justify-items: center; gap: 6px;
  color: rgba(255, 255, 255, 0.5);
  background: linear-gradient(150deg, rgba(48, 52, 86, 0.85), rgba(24, 24, 42, 0.95));
}
.cover-fallback.image { background: linear-gradient(150deg, rgba(70, 48, 86, 0.85), rgba(26, 22, 44, 0.95)); }
.cover-fallback em { font-style: normal; font-size: 11px; }
.badge {
  position: absolute; top: 8px; left: 8px; padding: 3px 7px; border-radius: 4px;
  font-size: 10px; backdrop-filter: blur(6px);
  background: rgba(10, 10, 20, 0.62); color: rgba(255, 255, 255, 0.85);
}
.badge.warn { background: rgba(251, 113, 133, 0.22); color: #ffc2cc; }
.badge.ok { background: rgba(52, 211, 153, 0.2); color: #a7f3d0; }
.likes {
  position: absolute; left: 8px; bottom: 8px; display: inline-flex; align-items: center; gap: 4px;
  font-size: 12px; font-weight: 500; color: #fff;
  text-shadow: 0 1px 4px rgba(0, 0, 0, 0.7);
}
.watch { position: absolute; left: 0; right: 0; bottom: 0; height: 3px; background: rgba(255, 255, 255, 0.24); }
.watch i { display: block; height: 100%; background: var(--vp-im-accent); }
.pick { position: absolute; top: 8px; right: 8px; }
.pick input {
  appearance: none; width: 18px; height: 18px; margin: 0; cursor: pointer;
  border: 1.5px solid rgba(255, 255, 255, 0.7); border-radius: 4px;
  background: rgba(10, 10, 20, 0.55); box-shadow: none;
}
.pick input:checked {
  background: var(--vp-im-accent); border-color: var(--vp-im-accent);
}
.pick input:checked::after {
  content: ''; display: block; width: 5px; height: 9px; margin: 1px 0 0 5px;
  border: solid #fff; border-width: 0 2px 2px 0; transform: rotate(45deg);
}
.card.pickable { cursor: pointer; }
.card.pickable .cover { border-color: rgba(255, 255, 255, 0.22); }
.card.picked .cover { border-color: var(--vp-im-accent); box-shadow: 0 0 0 2px rgba(255, 46, 126, 0.35); }
.caption {
  font-size: 13px; line-height: 1.4; color: var(--vp-im-text-primary);
  text-decoration: none; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.caption:hover { color: var(--vp-im-accent); }
.author { font-size: 11px; color: var(--vp-im-text-secondary); }
.appeal-state { font-size: 11px; color: #fbbf24; }
.appeal-mini {
  justify-self: start; padding: 3px 10px; border-radius: 99px; cursor: pointer; font-size: 11px;
  border: 1px solid rgba(255, 46, 126, 0.5); background: transparent; color: var(--vp-im-accent);
}
.empty {
  max-width: 1560px; margin: 24px auto; padding: 0 28px; text-align: center;
  font-size: 13px; color: var(--vp-im-text-secondary);
}

.gate { max-width: 820px; margin: 60px auto; text-align: center; }
.gate p { margin: 0 0 14px; font-size: 13px; color: var(--vp-im-text-secondary); }
.cta {
  border: 0; cursor: pointer; font-family: inherit;
  display: inline-block; padding: 8px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: #fff; font-size: 13px; text-decoration: none;
}
.notice { max-width: 1560px; margin: 12px auto 0; padding: 0 28px; font-size: 12px; color: var(--vp-im-accent); }
.hint { max-width: 1560px; margin: 24px auto; padding: 0 28px; text-align: center; font-size: 13px; color: var(--vp-im-text-secondary); }

/* 弹窗壳由 VpDialog 提供；这里只保留表单内部样式。 */
.tip { margin: 0 0 12px; font-size: 12px; line-height: 1.7; color: var(--vp-tool-text-2); }
textarea {
  width: 100%; box-sizing: border-box; padding: 10px 12px; border-radius: 10px; resize: vertical;
  border: 1px solid var(--vp-tool-input-border);
  background: var(--vp-tool-input); color: var(--vp-tool-text);
  font-size: 13px; line-height: 1.6; font-family: inherit;
}
.error { margin: 8px 0 0; font-size: 12px; color: var(--vp-tool-danger-ink); }
.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; }
.ghost, .primary-btn {
  padding: 8px 16px; border-radius: 99px; font-size: 13px; cursor: pointer;
  border: 1px solid var(--vp-tool-border-strong); background: transparent;
  color: var(--vp-tool-text);
}
.primary-btn { border: 0; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); }
.primary-btn.danger-btn { background: #e5484d; color: #fff; }
.primary-btn:disabled { opacity: 0.6; cursor: not-allowed; }

@media (max-width: 1400px) { .grid { grid-template-columns: repeat(5, minmax(0, 1fr)); } }
@media (max-width: 1100px) { .grid { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 780px) {
  .hero { padding: 12px 16px 18px; }
  .hero-top { margin-bottom: 18px; }
  .quick a:not(.publish) { display: none; }
  .hero-body { gap: 14px; }
  .avatar { width: 76px; height: 76px; font-size: 26px; }
  .name strong { font-size: 17px; }
  .counts { gap: 14px; }
  .tabbar, .grid, .search-row, .notice, .hint { padding-left: 16px; padding-right: 16px; }
  .grid { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px 10px; }
  .tabs button { font-size: 13px; padding: 12px 9px 11px; }
  .search-row { justify-content: stretch; }
  .search { width: 100%; }
}
@media (max-width: 480px) {
  .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .chips a:last-child { display: none; }
}
</style>
