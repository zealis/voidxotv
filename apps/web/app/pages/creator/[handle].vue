<script setup lang="ts">
import type { FeedItem } from '~/composables/useFeed'
import { ApiError, errorMessage } from '~/composables/useApi'

interface CreatorProfile {
  handle: string
  display_name: string
  /** 已认证创作者（users.role = 'creator'）。 */
  verified_creator?: boolean
  bio: string
  avatar_url: string
  content_count: number
  like_count: number
  following_count: number
  follower_count: number
}

const route = useRoute()
const handle = computed(() => String(route.params.handle || ''))

// 夜街号补零到 6 位：/creator/34 与 /creator/000034 是同一个人。
// 页面显示与「是否本人」判断都用补零后的值，避免旧链接把号显示成 34。
function padNightId(value: string): string {
  const trimmed = value.trim()
  if (!/^\d+$/.test(trimmed) || trimmed.length >= 6) return trimmed
  return trimmed.padStart(6, '0')
}
const { data, error } = await useAsyncData(
  () => `creator-${handle.value}`,
  () => $fetch<{ data: { profile: CreatorProfile; items: FeedItem[]; count: number } }>(
    `/api/v1/creators/${encodeURIComponent(handle.value)}`,
  ),
  { watch: [handle] },
)

const profile = computed(() => data.value?.data.profile || null)
// 补零后的夜街号：优先用接口返回的 handle，URL 里是 34 时也能显示成 000034。
const nightId = computed(() => profile.value?.handle || padNightId(handle.value))
const items = computed(() => data.value?.data.items || [])

// 公开作品按「我」页的卡片口径映射，供同一套分区 / 网格样式复用。
const query = ref('')
const cards = computed(() => {
  const list = items.value.map((item) => ({
    slug: item.slug,
    type: item.type,
    title: item.title || '（无标题）',
    poster: item.media?.poster_url,
    likes: item.stats?.likes || 0,
    href: item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`,
  }))
  const q = query.value.trim().toLowerCase()
  return q ? list.filter((card) => card.title.toLowerCase().includes(q)) : list
})

function formatCount(value: number): string {
  const n = value || 0
  if (n >= 10000) return `${(n / 10000).toFixed(1).replace(/\.0$/, '')}万`
  return String(n)
}

// 关注状态是「当前访客」的视角，不能进 SSR 结果（创作者页会被 Delivery 预渲染并公共缓存），
// 所以挂载后再单独取一次，只影响本机 UI。
const { api, getMe } = useApi()

const { open: openAuthDialog } = useAuthDialog()
const { followState, follow } = useFeed()

const following = ref(false)
const followerCount = ref(0)
const isSelf = ref(false)
const followBusy = ref(false)
const followError = ref('')

onMounted(async () => {
  followerCount.value = profile.value?.follower_count || 0
  const me = await getMe()
  if (!me) return
  if (me.handle === nightId.value) {
    isSelf.value = true
    return
  }
  try {
    const state = await followState(handle.value)
    following.value = state.following
    followerCount.value = state.follower_count
  } catch {
    // 未登录或接口暂不可用时保持默认展示，不影响作品浏览。
  }
})

async function toggleFollow() {
  if (followBusy.value) return
  followBusy.value = true
  followError.value = ''
  try {
    const state = await follow(handle.value, !following.value)
    following.value = state.following
    followerCount.value = state.follower_count
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可关注创作者', redirect: route.fullPath })
      return
    }
    followError.value = errorMessage(error)
  } finally {
    followBusy.value = false
  }
}

if (error.value || !profile.value) {
  throw createError({ statusCode: 404, statusMessage: '创作者不存在' })
}

// canonical 按请求头推导公开 origin（代理/CDN 后仍正确）。
const site = useSiteOrigin()
useSeoMeta({
  title: () => `${profile.value?.display_name || '用户' + nightId.value} · 夜街`,
  description: () => `夜街号：${nightId.value} 的公开作品，共 ${profile.value?.content_count || 0} 条。`,
  ogTitle: () => `${profile.value?.display_name || '用户' + nightId.value} · 夜街`,
  ogDescription: () => `夜街号：${nightId.value} 的公开作品`,
  ogImage: () => profile.value?.avatar_url || '',
  robots: 'index,follow',
})
useHead({
  link: [{ rel: 'canonical', href: `${site}/creator/${handle.value}` }],
})
</script>

<template>
  <main class="creator" data-surface="immersive">
    <header class="hero" aria-label="创作者资料">
      <div class="hero-top">
        <NuxtLink to="/" class="back"><ImIcon name="back" :size="14" /> 首页</NuxtLink>
        <div class="hero-actions">
          <button
            v-if="!isSelf" type="button" class="follow" :class="{ on: following }"
            :disabled="followBusy" @click="toggleFollow"
          >
            {{ followBusy ? '处理中…' : following ? '已关注' : '关注' }}
          </button>
        </div>
      </div>

      <div class="hero-body">
        <span class="avatar">
          <img v-if="profile?.avatar_url" :src="profile.avatar_url" :alt="`${nightId} 的头像`" />
          <span v-else>{{ nightId.slice(0, 1).toUpperCase() }}</span>
        </span>

        <div class="who">
          <div class="name">
            <strong>
              {{ profile?.display_name || `用户${nightId}` }}
              <CreatorBadge v-if="profile?.verified_creator" size="16" />
            </strong>
          </div>

          <div class="counts" aria-label="创作者数据">
            <span><span>作品</span><strong>{{ profile?.content_count || 0 }}</strong></span>
            <span><span>获赞</span><strong>{{ profile?.like_count || 0 }}</strong></span>
            <span><span>关注</span><strong>{{ profile?.following_count || 0 }}</strong></span>
            <span><span>粉丝</span><strong>{{ followerCount }}</strong></span>
          </div>

          <p class="handle">夜街号：{{ nightId }}</p>
          <p v-if="profile?.bio" class="bio">{{ profile.bio }}</p>
          <p v-if="followError" class="follow-err" role="alert">{{ followError }}</p>
        </div>
      </div>
    </header>

    <div class="tabbar">
      <nav class="tabs" aria-label="作品分区">
        <!-- 访客只有公开作品一个分区：用非按钮的分区标题沿用「我」页 tabbar 样式，
             不做点了没反应的死按钮（私密分区的数据访客本来也拿不到）。 -->
        <span class="tab-current">作品 <small>{{ cards.length }}</small></span>
      </nav>
    </div>

    <div class="search-row">
      <label class="search">
        <ImIcon name="search" :size="14" />
        <input v-model="query" type="search" placeholder="搜索 TA 的作品" aria-label="搜索 TA 的作品" />
      </label>
    </div>

    <div v-if="cards.length" class="grid">
      <article v-for="card in cards" :key="card.slug" class="card">
        <NuxtLink class="cover" :to="card.href" :aria-label="card.title">
          <img v-if="card.poster" :src="card.poster" alt="" loading="lazy" />
          <span v-else class="cover-fallback" :class="card.type">
            <ImIcon :name="card.type === 'image' ? 'image' : 'play'" :size="26" />
            <em>{{ card.type === 'image' ? '图集' : '视频' }}</em>
          </span>
          <span class="likes">
            <ImIcon name="heart" :size="13" filled /> {{ formatCount(card.likes) }}
          </span>
        </NuxtLink>
        <NuxtLink class="caption" :to="card.href" :title="card.title">{{ card.title }}</NuxtLink>
      </article>
    </div>
    <p v-else class="empty">{{ query ? '没有匹配的作品。' : '这位创作者还没有公开作品。' }}</p>
  </main>
</template>

<style scoped>
/* 与「我」页共用同一套个人主页视觉：渐变 hero + 分区条 + 搜索 + 6 列作品网格。
   访客视角去掉编辑资料 / 草稿箱 / 批量管理等私密入口，右上换成「关注」。
   样式保留为 scoped 副本，不把 .hero / .card / .grid 这类通用类名提到全局。 */
.creator {
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
  position: absolute; inset: 0; pointer-events: none;
  background: linear-gradient(118deg, transparent 46%, rgba(255, 255, 255, 0.045) 58%, transparent 70%);
}
.hero-top, .hero-body { position: relative; z-index: 1; max-width: 1560px; margin: 0 auto; }
.hero-top { display: flex; align-items: center; gap: 12px; margin-bottom: 26px; }
.back {
  display: inline-flex; align-items: center; gap: 3px;
  color: rgba(255, 255, 255, 0.72); text-decoration: none; font-size: 12px;
}
.back:hover { color: #fff; }
.hero-actions { margin-left: auto; display: flex; align-items: center; gap: 8px; }
.follow {
  padding: 5px 16px; border-radius: 99px; cursor: pointer; font-size: 12px;
  border: 1px solid var(--vp-im-accent);
  background: var(--vp-im-accent); color: #fff; font-weight: 600;
}
.follow.on {
  background: transparent; color: var(--vp-im-text-primary);
  border-color: rgba(255, 255, 255, 0.22); font-weight: 500;
}
.follow:disabled { opacity: 0.6; cursor: not-allowed; }
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
.counts { display: flex; align-items: baseline; gap: 22px; }
.counts > * {
  display: inline-flex; align-items: baseline; gap: 5px;
  color: rgba(255, 255, 255, 0.72); font-size: 12px;
}
.counts strong { font-size: 14px; font-weight: 600; color: #fff; }
.handle { margin: 0; font-size: 12px; color: rgba(255, 255, 255, 0.6); }
.bio {
  margin: 0; font-size: 12px; line-height: 1.6;
  color: rgba(255, 255, 255, 0.86); overflow-wrap: anywhere;
}
.follow-err { margin: 0; font-size: 11px; color: #ff9d94; }

.tabbar {
  max-width: 1560px; margin: 0 auto; padding: 0 28px;
  display: flex; align-items: center; gap: 16px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.07);
}
.tabs { display: flex; align-items: center; gap: 4px; min-width: 0; }
.tab-current {
  position: relative; display: inline-flex; align-items: center; gap: 5px;
  padding: 14px 12px 13px; color: var(--vp-im-text-primary);
  font-size: 14px; font-weight: 600; white-space: nowrap;
}
.tab-current small { font-size: 12px; opacity: 0.75; }
.tab-current::after {
  content: ''; position: absolute; left: 12px; right: 12px; bottom: 0;
  height: 3px; border-radius: 3px 3px 0 0; background: var(--vp-im-accent);
}
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
.likes {
  position: absolute; left: 8px; bottom: 8px; display: inline-flex; align-items: center; gap: 4px;
  font-size: 12px; font-weight: 500; color: #fff;
  text-shadow: 0 1px 4px rgba(0, 0, 0, 0.7);
}
.caption {
  font-size: 13px; line-height: 1.4; color: var(--vp-im-text-primary);
  text-decoration: none; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.caption:hover { color: var(--vp-im-accent); }
.empty {
  max-width: 1560px; margin: 24px auto; padding: 0 28px; text-align: center;
  font-size: 13px; color: var(--vp-im-text-secondary);
}

@media (max-width: 1400px) { .grid { grid-template-columns: repeat(5, minmax(0, 1fr)); } }
@media (max-width: 1100px) { .grid { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 780px) {
  .hero { padding: 12px 16px 18px; }
  .hero-top { margin-bottom: 18px; }
  .hero-body { gap: 14px; }
  .avatar { width: 76px; height: 76px; font-size: 26px; }
  .name strong { font-size: 17px; }
  .counts { gap: 14px; }
  .tabbar, .grid, .search-row { padding-left: 16px; padding-right: 16px; }
  .grid { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px 10px; }
  .search-row { justify-content: stretch; }
  .search { width: 100%; }
}
@media (max-width: 480px) {
  .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
