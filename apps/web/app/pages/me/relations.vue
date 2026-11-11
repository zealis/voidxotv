<script setup lang="ts">
// 「关注 / 粉丝 / 赞过我」三类关系列表（UI 规范 §10：
// 计数区（获赞 / 关注 / 粉丝）点击进入对应列表）。
import { ApiError, errorMessage } from '~/composables/useApi'

useSeoMeta({ title: '关系 · 夜街', robots: 'noindex,nofollow' })

type RelationKind = 'following' | 'followers' | 'likes'

interface RelationItem {
  id: number
  handle: string
  display_name: string
  bio: string
  follower_count: number
  following: boolean
  followed_by: boolean
}

const route = useRoute()
const router = useRouter()
const { api, getMe } = useApi()
const { follow } = useFeed()

const tabs: { key: RelationKind; label: string }[] = [
  { key: 'following', label: '关注' },
  { key: 'followers', label: '粉丝' },
  { key: 'likes', label: '赞过我' },
]

function readTab(): RelationKind {
  const value = String(route.query.tab || '')
  return value === 'followers' || value === 'likes' ? value : 'following'
}

const tab = ref<RelationKind>(readTab())
const items = ref<RelationItem[]>([])
const total = ref(0)
const hasMore = ref(false)
const loading = ref(false)
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')
const busy = ref<Record<string, boolean>>({})

const emptyText = computed(() => ({
  following: '还没有关注任何人，去沉浸流里找几位喜欢的作者。',
  followers: '还没有粉丝，发布作品会让更多人看到你。',
  likes: '还没有人赞过你的作品。',
}[tab.value]))

function avatarOf(item: RelationItem): string {
  // 与信息流一致：没有上传头像时按 id 取演示图，避免空白圆。
  return `/demo/c${(item.id % 12) + 1}.jpeg`
}

// 昵称为空时标题直接用 @handle，副标题就不再重复账号；
// 有昵称时副标题才补 @handle，避免同一行出现两遍相同文字。
function titleOf(item: RelationItem): string {
  return item.display_name || `@${item.handle}`
}

function subtitleOf(item: RelationItem): string {
  const parts: string[] = []
  if (item.display_name) parts.push(`@${item.handle}`)
  if (item.bio) parts.push(item.bio)
  return parts.join(' · ')
}

async function load(reset = true) {
  loading.value = true
  message.value = ''
  try {
    const offset = reset ? 0 : items.value.length
    const data = await api<{ items: RelationItem[]; total: number; has_more: boolean }>(
      `/api/v1/me/relations?kind=${tab.value}&limit=20&offset=${offset}`,
    )
    items.value = reset ? data.items : [...items.value, ...data.items]
    total.value = data.total
    hasMore.value = data.has_more
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) needLogin.value = true
    else message.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

function switchTab(next: RelationKind) {
  if (next === tab.value) return
  tab.value = next
  items.value = []
  total.value = 0
  hasMore.value = false
  void router.replace({ query: { ...route.query, tab: next } })
  void load(true)
}

async function toggleFollow(item: RelationItem) {
  if (busy.value[item.handle]) return
  busy.value = { ...busy.value, [item.handle]: true }
  try {
    const state = await follow(item.handle, !item.following)
    item.following = state.following
    item.follower_count = state.follower_count
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
      return
    }
    message.value = errorMessage(error)
  } finally {
    busy.value = { ...busy.value, [item.handle]: false }
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
</script>

<template>
  <main class="relations" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/me" class="back" aria-label="返回我的页面">←</NuxtLink>
      <h1>关系</h1>
      <span v-if="!loading && total" class="total">共 {{ total }} 人</span>
    </header>

    <nav class="tabs" aria-label="关系类型">
      <button
        v-for="entry in tabs" :key="entry.key" type="button"
        :class="{ on: tab === entry.key }" :aria-current="tab === entry.key ? 'page' : undefined"
        @click="switchTab(entry.key)"
      >
        {{ entry.label }}
      </button>
    </nav>

    <p v-if="message" class="notice" role="alert">{{ message }}</p>
    <p v-if="checking || loading && !items.length" class="hint">加载中…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后查看关注、粉丝与获赞记录。</p>
      <NuxtLink to="/login?redirect=%2Fme%2Frelations" class="cta">登录</NuxtLink>
    </section>

    <template v-else>
      <ul v-if="items.length" class="people">
        <li v-for="item in items" :key="item.id">
          <NuxtLink :to="`/creator/${item.handle}`" class="who">
            <img class="avatar" :src="avatarOf(item)" alt="" loading="lazy">
            <span class="meta">
              <span class="name">
                {{ titleOf(item) }}
                <span v-if="item.followed_by && item.following" class="mutual">互关</span>
                <span v-else-if="item.followed_by && !item.following" class="mutual">关注了你</span>
              </span>
              <span v-if="subtitleOf(item)" class="sub">{{ subtitleOf(item) }}</span>
            </span>
          </NuxtLink>
          <button
            type="button" class="follow" :class="{ on: item.following }"
            :disabled="busy[item.handle]"
            @click="toggleFollow(item)"
          >
            {{ item.following ? '已关注' : item.followed_by ? '回关' : '关注' }}
          </button>
        </li>
      </ul>
      <p v-else class="hint">{{ emptyText }}</p>

      <button v-if="hasMore" type="button" class="more" :disabled="loading" @click="load(false)">
        {{ loading ? '加载中…' : '加载更多' }}
      </button>
    </template>
  </main>
</template>

<style scoped>
.relations {
  min-height: 100%;
  padding: 18px 18px calc(40px + env(safe-area-inset-bottom));
  box-sizing: border-box;
  color: var(--vp-im-text-primary);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: 720px; margin: 0 auto 14px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.total { margin-left: auto; font-size: 12px; color: var(--vp-im-text-secondary); }

.tabs { max-width: 720px; margin: 0 auto 12px; display: flex; gap: 6px; }
.tabs button {
  padding: 6px 16px; border: 0; border-radius: 99px; cursor: pointer;
  background: rgba(255, 255, 255, 0.06); color: var(--vp-im-text-secondary);
  font-size: 12px;
}
.tabs button.on { background: rgba(255, 46, 126, 0.16); color: var(--vp-im-accent); font-weight: 600; }

.people {
  list-style: none; margin: 0 auto; padding: 0; max-width: 720px;
  display: grid; gap: 8px;
}
.people li {
  display: flex; align-items: center; gap: 12px;
  padding: 10px 14px; border-radius: 14px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.06);
}
.who { flex: 1; min-width: 0; display: flex; align-items: center; gap: 12px; text-decoration: none; color: inherit; }
.avatar { width: 42px; height: 42px; border-radius: 50%; object-fit: cover; flex: none; background: rgba(255, 255, 255, 0.08); }
.meta { min-width: 0; display: grid; gap: 3px; }
.name { font-size: 14px; display: flex; align-items: center; gap: 6px; }
.mutual {
  padding: 1px 6px; border-radius: 4px; font-size: 10px;
  color: #26deed; border: 1px solid currentColor;
}
.sub {
  font-size: 11px; color: var(--vp-im-text-secondary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}

.follow {
  flex: none; padding: 6px 16px; border-radius: 99px; cursor: pointer;
  border: 0; background: var(--vp-im-accent); color: #fff; font-size: 12px; font-weight: 600;
}
.follow.on {
  background: transparent; color: var(--vp-im-text-secondary);
  border: 1px solid rgba(255, 255, 255, 0.18); font-weight: 500;
}
.follow:disabled { opacity: 0.6; cursor: not-allowed; }

.hint { max-width: 720px; margin: 40px auto; text-align: center; font-size: 13px; color: var(--vp-im-text-secondary); }
.notice {
  max-width: 720px; margin: 0 auto 12px; padding: 10px 12px; border-radius: 10px;
  background: rgba(255, 105, 97, 0.14); color: #ffb4ae; font-size: 12px;
}
.gate { max-width: 720px; margin: 40px auto; text-align: center; color: var(--vp-im-text-secondary); font-size: 13px; }
.gate .cta {
  display: inline-block; margin-top: 12px; padding: 9px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: #fff; text-decoration: none; font-size: 13px;
}
.more {
  display: block; margin: 16px auto 0; padding: 9px 22px; border-radius: 99px;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-primary); font-size: 12px; cursor: pointer;
}
</style>
