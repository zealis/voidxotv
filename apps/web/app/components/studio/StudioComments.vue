<script setup lang="ts">
// 创作者中心 · 评论管理（§11.1）：列出自己作品下的评论并支持删除（软删除）。
import { errorMessage } from '~/composables/useApi'

interface ManagedComment {
  id: number
  content_id: number
  content_title: string
  content_slug: string
  content_type: 'video' | 'image'
  handle: string
  body: string
  created_at: string
}

const { api } = useApi()
const asyncState = useAsyncState({
  keepOnError: true,
  isEmpty: () => items.value.length === 0,
})
const { phase, errorText, partial, retrying } = asyncState

const items = ref<ManagedComment[]>([])
const total = ref(0)
const hasMore = ref(false)
const loading = ref(true)
const message = ref('')
const saved = ref('')
const busy = ref<Record<number, boolean>>({})
const confirming = ref<number | null>(null)
let savedTimer: ReturnType<typeof setTimeout> | null = null

function timeText(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function flash(text: string) {
  saved.value = text
  if (savedTimer) clearTimeout(savedTimer)
  savedTimer = setTimeout(() => { saved.value = '' }, 1800)
}

async function load(reset = true) {
  loading.value = true
  message.value = ''
  await asyncState.run(async () => {
    const offset = reset ? 0 : items.value.length
    const data = await api<{ items: ManagedComment[]; total: number; has_more: boolean }>(
      `/api/v1/me/comments?limit=20&offset=${offset}`,
    )
    items.value = reset ? data.items : [...items.value, ...data.items]
    total.value = data.total
    hasMore.value = data.has_more
    return data
  })
  loading.value = false
}

async function remove(comment: ManagedComment) {
  if (busy.value[comment.id]) return
  busy.value = { ...busy.value, [comment.id]: true }
  message.value = ''
  try {
    await api(`/api/v1/contents/${comment.content_id}/comments/${comment.id}`, { method: 'DELETE' })
    items.value = items.value.filter((entry) => entry.id !== comment.id)
    total.value = Math.max(0, total.value - 1)
    confirming.value = null
    flash('评论已删除')
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    busy.value = { ...busy.value, [comment.id]: false }
  }
}

onMounted(() => { void load() })
onBeforeUnmount(() => { if (savedTimer) clearTimeout(savedTimer) })
</script>

<template>
  <section class="comments-panel">
    <header class="head">
      <h2>评论管理</h2>
      <span v-if="saved" class="saved" role="status">{{ saved }}</span>
      <span v-else-if="total" class="meta">共 {{ total }} 条</span>
    </header>

    <p v-if="message" class="notice" role="alert">{{ message }}</p>
    <VpAsyncState
      :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="4"
      empty-title="还没有收到评论"
      empty-hint="作品收到评论后，可以在这里查看并管理。"
      @retry="load()"
    >
      <ul class="list">
        <li v-for="comment in items" :key="comment.id">
          <div class="line">
            <span class="who">@{{ comment.handle }}</span>
            <span class="time">{{ timeText(comment.created_at) }}</span>
          </div>
          <p class="body">{{ comment.body }}</p>
          <div class="line foot">
            <NuxtLink class="work" :to="`/${comment.content_type}/${comment.content_slug}`">
              {{ comment.content_title || '（无标题作品）' }}
            </NuxtLink>
            <span class="actions">
              <template v-if="confirming === comment.id">
                <button type="button" class="btn danger" :disabled="busy[comment.id]" @click="remove(comment)">
                  确认删除
                </button>
                <button type="button" class="btn" @click="confirming = null">取消</button>
              </template>
              <button v-else type="button" class="btn" @click="confirming = comment.id">删除</button>
            </span>
          </div>
        </li>
      </ul>
      <button v-if="hasMore" type="button" class="more" :disabled="loading" @click="load(false)">
        {{ loading ? '加载中…' : '加载更多' }}
      </button>
    </VpAsyncState>
  </section>
</template>

<style scoped>
.comments-panel { max-width: var(--vp-layout-content-max); margin: 0 auto; }
.head { display: flex; align-items: baseline; gap: 10px; margin-bottom: 10px; }
.head h2 { margin: 0; font-size: 13px; font-weight: 600; }
.meta, .saved { margin-left: auto; font-size: 11px; color: var(--vp-tool-text-2); }
.saved { color: var(--vp-tool-accent-ink); }

.list { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
.list li {
  padding: 10px 14px; border-radius: 14px;
  background: var(--vp-tool-surface);
  border: 1px solid var(--vp-tool-border);
}
.line { display: flex; align-items: center; gap: 8px; }
.who { font-size: 12px; color: var(--vp-tool-text); }
.time { font-size: 10px; color: var(--vp-tool-text-2); }
.body { margin: 6px 0 8px; font-size: 13px; line-height: 1.55; word-break: break-word; }
.foot { gap: 10px; }
.work {
  flex: 1; min-width: 0; font-size: 11px; color: var(--vp-tool-text-2);
  text-decoration: none; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.work:hover { color: var(--vp-tool-accent-ink); }
.actions { display: flex; gap: 6px; margin-left: auto; }
.btn {
  padding: 5px 11px; border-radius: 99px; cursor: pointer; font-size: 11px;
  border: 1px solid var(--vp-tool-border-strong); background: transparent;
  color: var(--vp-tool-text);
}
.btn.danger { border-color: var(--vp-tool-danger-border); color: var(--vp-tool-danger-ink); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }

.notice {
  margin: 0 0 10px; padding: 9px 12px; border-radius: 10px;
  background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink); font-size: 12px;
}
.hint { margin: 30px 0; text-align: center; font-size: 12px; color: var(--vp-tool-text-2); }
.more {
  display: block; margin: 14px auto 0; padding: 8px 20px; border-radius: 99px;
  border: 1px solid var(--vp-tool-border-strong); background: transparent;
  color: var(--vp-tool-text); font-size: 12px; cursor: pointer;
}
</style>
