<script setup lang="ts">
// 话题列表页（§4.1 /topic）：公开可浏览的 available 话题，按作品数排序，
// 点击进入话题页回到沉浸流。公开页：可索引、SWR。
interface TopicCard {
  id: number
  slug: string
  name: string
  content_count: number
}

const { api } = useApi()
const asyncState = useAsyncState({ keepOnError: true, isEmpty: () => topics.value.length === 0 })
const { phase, errorText, partial, retrying } = asyncState
const topics = ref<TopicCard[]>([])

async function load() {
  asyncState.begin()
  try {
    const data = await api<{ items: TopicCard[] }>('/api/v1/topics?limit=60')
    topics.value = data.items || []
    asyncState.succeed()
  } catch (error) {
    asyncState.fail(error)
  }
}

onMounted(load)

useSeoMeta({
  title: '话题 · 夜街',
  description: '浏览夜街上的热门话题，进入话题页看相关作品。',
})
</script>

<template>
  <main class="topics" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/" class="back" aria-label="回到沉浸流">←</NuxtLink>
      <h1>话题</h1>
    </header>

    <VpAsyncState
      :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="4"
      empty-title="还没有可浏览的话题"
      empty-hint="发布作品时可以自建话题，审核通过后会出现在这里。"
      @retry="load"
    >
      <ul class="grid">
        <li v-for="topic in topics" :key="topic.id">
          <NuxtLink :to="`/topic/${topic.slug}`" class="card">
            <span class="hash">#</span>
            <span class="name">{{ topic.name }}</span>
            <span class="count">{{ topic.content_count }} 个作品</span>
          </NuxtLink>
        </li>
      </ul>
    </VpAsyncState>
  </main>
</template>

<style scoped>
.topics {
  min-height: 100dvh;
  padding: 20px 20px 56px;
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.bar {
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: 760px;
  margin: 0 auto 18px;
}
.back {
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary);
  text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.grid {
  list-style: none;
  margin: 0 auto;
  padding: 0;
  max-width: 760px;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 10px;
}
.card {
  display: grid;
  grid-template-columns: auto 1fr;
  align-items: baseline;
  gap: 2px 8px;
  padding: 14px 16px;
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.06);
  color: var(--vp-im-text-primary);
  text-decoration: none;
}
.hash { font-size: 18px; font-weight: 700; color: var(--vp-im-accent); grid-row: span 2; }
.name { font-size: 14px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.count { grid-column: 2; font-size: 11px; color: var(--vp-im-text-secondary); }
</style>
