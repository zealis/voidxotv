<script setup lang="ts">
// 分类详情：该分类下的公开作品以沉浸流方式消费（§4.1 /search 与 /category 说明）。
// 页面数据全部来自公开接口，SSR 直出，可被搜索引擎索引。
import type { FeedItem } from '~/composables/useFeed'

interface CategoryProfile {
  id: number
  slug: string
  name: string
  description: string
  content_count: number
}

const route = useRoute()
const slug = computed(() => String(route.params.slug || ''))

const { data, error } = await useAsyncData(
  () => `category-${slug.value}`,
  () => $fetch<{ data: { profile: CategoryProfile; items: FeedItem[]; count: number } }>(
    `/api/v1/categories/${encodeURIComponent(slug.value)}`,
  ),
  { watch: [slug] },
)

const profile = computed(() => data.value?.data.profile || null)
const items = computed(() => data.value?.data.items || [])

if (error.value || !profile.value) {
  throw createError({ statusCode: 404, statusMessage: '分类不存在' })
}

// canonical 按请求头推导公开 origin（代理/CDN 后仍正确）。
const site = useSiteOrigin()
useSeoMeta({
  title: () => `${profile.value?.name || '分类'} · 夜街`,
  description: () => profile.value?.description
    || `${profile.value?.name || '分类'}下的夜街公开作品，共 ${profile.value?.content_count || 0} 条。`,
  ogTitle: () => `${profile.value?.name || '分类'} · 夜街`,
  ogDescription: () => profile.value?.description || '夜街分类',
  robots: 'index,follow',
})
useHead({
  link: [{ rel: 'canonical', href: `${site}/category/${slug.value}` }],
})
</script>

<template>
  <main class="category" data-surface="immersive">
    <header class="hero">
      <div class="identity">
        <p class="eyebrow">分类</p>
        <h1>{{ profile?.name }}</h1>
        <p class="desc">{{ profile?.description || '该分类下的公开作品。' }}</p>
        <p class="stats">{{ profile?.content_count || 0 }} 条公开作品</p>
      </div>
      <div class="links">
        <NuxtLink to="/search" class="pill">去搜索</NuxtLink>
        <NuxtLink to="/" class="pill">沉浸流</NuxtLink>
      </div>
    </header>

    <section class="grid" aria-label="分类下的公开作品">
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
          <p class="meta">@{{ item.author.handle }}</p>
          <p class="caption">{{ item.description || '暂无文案' }}</p>
        </NuxtLink>
      </article>
    </section>

    <p v-if="!items.length" class="empty">这个分类下暂时没有公开作品。</p>
  </main>
</template>

<style scoped>
.category {
  min-height: 100dvh;
  padding: 32px 24px 56px;
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.hero {
  display: flex; align-items: flex-end; gap: 20px;
  max-width: 1080px; margin: 0 auto 28px;
  padding-bottom: 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.eyebrow {
  margin: 0 0 4px; font-size: 11px; letter-spacing: 1.6px;
  text-transform: uppercase; color: var(--vp-im-accent);
}
.identity h1 { margin: 0; font-size: 26px; }
.desc { margin: 6px 0 8px; font-size: 13px; color: var(--vp-im-text-secondary); }
.stats { margin: 0; font-size: 12px; color: var(--vp-im-text-secondary); }
.links { margin-left: auto; display: flex; gap: 8px; }
.pill {
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
.cover-fallback {
  position: absolute; inset: 0; display: grid; place-items: center;
  font-size: 13px; color: var(--vp-im-text-secondary);
}
.type {
  position: absolute; right: 8px; bottom: 8px;
  padding: 2px 8px; border-radius: 99px; font-size: 11px;
  background: rgba(0, 0, 0, 0.55); color: #fff;
}
.card h2 { margin: 0; font-size: 13px; font-weight: 600; line-height: 1.4; }
.meta { margin: 0; font-size: 11px; color: var(--vp-im-text-secondary); }
.caption {
  margin: 0; font-size: 12px; line-height: 1.5; color: var(--vp-im-text-secondary);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
.empty {
  max-width: 1080px; margin: 24px auto;
  font-size: 13px; color: var(--vp-im-text-secondary);
}
</style>
