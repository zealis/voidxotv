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
    <header class="hero">
      <div class="avatar" aria-hidden="true">
        <img v-if="profile?.avatar_url" :src="profile.avatar_url" :alt="`${handle} 的头像`">
        <span v-else>{{ handle.slice(0, 1).toUpperCase() }}</span>
      </div>
      <div class="identity">
        <h1>
          {{ profile?.display_name || `用户${handle}` }}
          <CreatorBadge v-if="profile?.verified_creator" size="18" />
        </h1>
        <p class="handle">夜街号：{{ nightId }}</p>
        <p v-if="profile?.bio" class="bio">{{ profile.bio }}</p>
        <p class="stats">
          <span>{{ profile?.content_count || 0 }} 作品</span>
          <span>{{ profile?.like_count || 0 }} 获赞</span>
          <span>{{ profile?.following_count || 0 }} 关注</span>
          <span>{{ followerCount }} 粉丝</span>
        </p>
        <p v-if="followError" class="follow-err" role="alert">{{ followError }}</p>
      </div>
      <div class="hero-actions">
        <button
          v-if="!isSelf" type="button" class="follow" :class="{ on: following }"
          :disabled="followBusy" @click="toggleFollow"
        >
          {{ followBusy ? '处理中…' : following ? '已关注' : '关注' }}
        </button>
        <NuxtLink to="/" class="back">回到沉浸流</NuxtLink>
      </div>
    </header>

    <section class="grid" aria-label="公开作品">
      <article v-for="item in items" :key="item.slug" class="card">
        <NuxtLink :to="item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`">
          <div class="cover">
            <img
              v-if="item.media?.poster_url" :src="item.media.poster_url"
              :alt="item.title || '作品封面'" loading="lazy"
            >
            <span v-else class="cover-fallback">{{ item.type === 'image' ? '图集' : '视频' }}</span>
            <span class="type">{{ item.type === 'image' ? '图集' : '视频' }}</span>
          </div>
          <h2>{{ item.title || '（无标题）' }}</h2>
          <p>{{ item.description || '暂无文案' }}</p>
        </NuxtLink>
      </article>
    </section>

    <p v-if="!items.length" class="empty">这位创作者还没有公开作品。</p>
  </main>
</template>

<style scoped>
.creator {
  min-height: 100dvh;
  padding: 32px 24px 56px;
  background: #0b0b0d;
  color: var(--vp-im-text-primary);
}
.hero {
  display: flex; align-items: center; gap: 16px;
  max-width: 1080px; margin: 0 auto 28px;
  padding-bottom: 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.avatar {
  width: 72px; height: 72px; border-radius: 50%; overflow: hidden;
  display: grid; place-items: center;
  background: rgba(255, 255, 255, 0.08);
  font-size: 24px; font-weight: 700; color: #fff;
}
.avatar img { width: 100%; height: 100%; object-fit: cover; }
/* 标题与认证标签同一行居中：inline 排布时标签会比文字基线低约 2px。 */
.identity h1 {
  display: flex; flex-wrap: wrap; align-items: center; justify-content: center;
  gap: 8px; margin: 0; font-size: 22px;
}
.handle { margin: 2px 0 6px; font-size: 12px; color: var(--vp-im-text-secondary); }
.bio {
  max-width: 520px; margin: 6px auto 10px; font-size: 12px; line-height: 1.6;
  color: var(--vp-im-text-secondary); white-space: pre-wrap;
}
.stats { margin: 0; display: flex; gap: 14px; font-size: 12px; color: var(--vp-im-text-secondary); }
.hero-actions { margin-left: auto; display: flex; align-items: center; gap: 8px; }
.follow {
  padding: 7px 18px; border: 0; border-radius: 99px; cursor: pointer;
  background: var(--vp-im-accent); color: #fff; font-size: 12px; font-weight: 600;
}
.follow.on {
  background: transparent; color: var(--vp-im-text-primary);
  border: 1px solid rgba(255, 255, 255, 0.22); font-weight: 500;
}
.follow:disabled { opacity: 0.6; cursor: not-allowed; }
.follow-err { margin: 6px 0 0; font-size: 11px; color: #ff9d94; }
.back {
  padding: 7px 14px; border-radius: 99px;
  border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); font-size: 12px; text-decoration: none;
}
.grid {
  max-width: 1080px; margin: 0 auto;
  display: grid; gap: 16px;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
}
.card a { text-decoration: none; color: inherit; display: grid; gap: 6px; }
.cover {
  position: relative; aspect-ratio: 3 / 4; border-radius: 14px; overflow: hidden;
  background: rgba(255, 255, 255, 0.05);
}
.cover img { width: 100%; height: 100%; object-fit: cover; }
.cover-fallback { position: absolute; inset: 0; display: grid; place-items: center; font-size: 13px; color: var(--vp-im-text-secondary); }
.type {
  position: absolute; right: 8px; bottom: 8px;
  padding: 2px 8px; border-radius: 99px; font-size: 11px;
  background: rgba(0, 0, 0, 0.55); color: #fff;
}
.card h2 { margin: 0; font-size: 13px; font-weight: 600; line-height: 1.4; }
.card p {
  margin: 0; font-size: 12px; line-height: 1.5; color: var(--vp-im-text-secondary);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
.empty { max-width: 1080px; margin: 24px auto; font-size: 13px; color: var(--vp-im-text-secondary); }
</style>
