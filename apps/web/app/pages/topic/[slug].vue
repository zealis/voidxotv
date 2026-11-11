<script setup lang="ts">
import type { FeedItem } from '~/composables/useFeed'

interface TopicProfile {
  slug: string
  name: string
  status: string
  content_count: number
}

const route = useRoute()
const slug = computed(() => String(route.params.slug || ''))

const { data, error } = await useAsyncData(
  () => `topic-${slug.value}`,
  () => $fetch<{ data: { profile: TopicProfile; items: FeedItem[]; count: number; note?: string } }>(
    `/api/v1/topics/${encodeURIComponent(slug.value)}`,
  ),
  { watch: [slug] },
)

const profile = computed(() => data.value?.data.profile || null)
const items = computed(() => data.value?.data.items || [])
const note = computed(() => data.value?.data.note || '')
const publicTopic = computed(() => profile.value?.status === 'available')

if (error.value || !profile.value) {
  throw createError({ statusCode: 404, statusMessage: '话题不存在' })
}

// canonical 按请求头推导公开 origin（代理/CDN 后仍正确）。
const site = useSiteOrigin()
useSeoMeta({
  title: () => `#${profile.value?.name || slug.value} · 夜街`,
  description: () => `夜街话题 #${profile.value?.name || slug.value} 下的公开作品，共 ${profile.value?.content_count || 0} 条。`,
  ogTitle: () => `#${profile.value?.name || slug.value} · 夜街`,
  robots: () => (publicTopic.value ? 'index,follow' : 'noindex,nofollow'),
})
useHead({
  link: [{ rel: 'canonical', href: `${site}/topic/${slug.value}` }],
})
</script>

<template>
  <main class="topic" data-surface="immersive">
    <header class="hero">
      <p class="eyebrow">话题</p>
      <h1>#{{ profile?.name }}</h1>
      <p class="stats">
        <span>{{ profile?.content_count || 0 }} 作品</span>
        <span v-if="!publicTopic" class="warn">该话题当前未公开</span>
      </p>
      <NuxtLink to="/" class="back">回到沉浸流</NuxtLink>
    </header>

    <p v-if="note" class="note">{{ note }}；既有作品关联仍保留。</p>

    <section v-if="items.length" class="grid" aria-label="话题作品">
      <article v-for="item in items" :key="item.slug" class="card">
        <NuxtLink :to="item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`">
          <div class="cover">
            <img
              v-if="item.media.poster_url" :src="item.media.poster_url"
              :alt="item.title || '作品封面'" loading="lazy"
            >
            <span v-else class="cover-fallback">{{ item.type === 'image' ? '图集' : '视频' }}</span>
          </div>
          <h2>{{ item.title || '（无标题）' }}</h2>
          <p class="author">@{{ item.author.handle }}</p>
        </NuxtLink>
      </article>
    </section>

    <p v-else-if="!note" class="empty">这个话题还没有公开作品。</p>
  </main>
</template>

<style scoped>
.topic {
  min-height: 100dvh;
  padding: 32px 24px 56px;
  background: #0b0b0d;
  color: var(--vp-im-text-primary);
}
.hero {
  position: relative;
  max-width: 1080px; margin: 0 auto 24px;
  padding-bottom: 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.eyebrow { margin: 0 0 6px; font-size: 11px; letter-spacing: 0.16em; color: var(--vp-im-accent); }
.hero h1 { margin: 0 0 6px; font-size: 26px; }
.stats { margin: 0; display: flex; gap: 14px; font-size: 12px; color: var(--vp-im-text-secondary); }
.warn { color: #fbbf24; }
.back {
  position: absolute; right: 0; top: 0;
  padding: 7px 14px; border-radius: 99px;
  border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); font-size: 12px; text-decoration: none;
}
.note {
  max-width: 1080px; margin: 0 auto 18px;
  padding: 12px 14px; border-radius: 12px;
  background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.24);
  font-size: 12px; color: #fcd34d;
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
.card h2 { margin: 0; font-size: 13px; font-weight: 600; line-height: 1.4; }
.author { margin: 0; font-size: 11px; color: var(--vp-im-text-secondary); }
.empty { max-width: 1080px; margin: 24px auto; font-size: 13px; color: var(--vp-im-text-secondary); }
</style>
