<script setup lang="ts">
// 评论抽屉（UI 规范 §6.5）：底部抽屉、分页列表、楼中楼回复（缩进 + 回复 @某人）、
// @handle 高亮、乐观更新（发送失败保留草稿并标红）、删除自己的评论。
// 数据全部来自服务端：评论落库后由后端派发评论 / 回复 / @提及 通知（§9.1 互动分组）。
import { nextTick, reactive, ref, watch } from 'vue'
import { ApiError, errorMessage } from '~/composables/useApi'

interface CommentItem {
  id: number
  body: string
  handle: string
  created_at: string
  user_id: number
  reply_count: number
  pending?: boolean
}

interface ReplyItem {
  id: number
  body: string
  handle: string
  created_at: string
  user_id: number
  reply_to?: string
  pending?: boolean
}

interface Thread {
  items: ReplyItem[]
  cursor: string | number | null
  open: boolean
  loading: boolean
  loaded: boolean
}

const props = defineProps<{ open: boolean; contentId: number; count: number }>()
const emit = defineEmits<{ close: []; 'count-change': [total: number] }>()

const { api, getMe } = useApi()

const items = ref<CommentItem[]>([])
const total = ref(props.count)
const cursor = ref<string | number | null>(null)
const loading = ref(false)
const posting = ref(false)
const draft = ref('')
const sentDraft = ref('')
const postFailed = ref(false)
const notice = ref('')
const replyTo = ref<{ id: number; handle: string } | null>(null)
const confirmDelete = ref<number | null>(null)
const me = ref<{ id: number; handle: string } | null>(null)
const threads = reactive<Record<number, Thread>>({})
const input = ref<HTMLInputElement | null>(null)
let confirmTimer: ReturnType<typeof setTimeout> | null = null

watch(() => props.open, async (open) => {
  if (!open) return
  await nextTick()
  input.value?.focus()
  if (!items.value.length) load(true)
  const who = await getMe()
  me.value = who ? { id: who.user_id, handle: who.handle } : null
})

watch(() => props.contentId, () => {
  // 切换条目后清空状态，避免上一个作品的评论串场。
  items.value = []
  cursor.value = null
  total.value = props.count
  Object.keys(threads).forEach((key) => delete threads[Number(key)])
  replyTo.value = null
  postFailed.value = false
  notice.value = ''
})

function setTotal(value: number) {
  total.value = value
  emit('count-change', value)
}

async function load(reset = true) {
  if (!props.contentId) return
  loading.value = true
  try {
    const suffix = reset || cursor.value === null ? '' : `&cursor=${encodeURIComponent(String(cursor.value))}`
    const data = await api<{ items: CommentItem[]; total: number; next_cursor: string | number | null }>(
      `/api/v1/contents/${props.contentId}/comments?limit=20${suffix}`,
    )
    items.value = reset ? data.items : [...items.value, ...data.items]
    cursor.value = data.next_cursor
    setTotal(data.total)
  } catch (error) {
    notice.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

function ensureThread(rootId: number): Thread {
  if (!threads[rootId]) {
    threads[rootId] = { items: [], cursor: null, open: false, loading: false, loaded: false }
  }
  return threads[rootId]!
}

async function toggleThread(rootId: number) {
  const thread = ensureThread(rootId)
  thread.open = !thread.open
  if (thread.open && !thread.loaded) await loadReplies(rootId)
}

async function loadReplies(rootId: number) {
  const thread = ensureThread(rootId)
  thread.loading = true
  try {
    const suffix = thread.cursor === null ? '' : `&cursor=${encodeURIComponent(String(thread.cursor))}`
    const data = await api<{ items: ReplyItem[]; next_cursor: string | number | null }>(
      `/api/v1/contents/${props.contentId}/comments/${rootId}/replies?limit=20${suffix}`,
    )
    thread.items = [...thread.items, ...data.items]
    thread.cursor = data.next_cursor
    thread.loaded = true
  } catch (error) {
    notice.value = errorMessage(error)
  } finally {
    thread.loading = false
  }
}

function startReply(handle: string, commentId: number) {
  replyTo.value = { id: commentId, handle }
  input.value?.focus()
}

function cancelReply() {
  replyTo.value = null
}

async function submit() {
  const text = draft.value.trim()
  if (!text || posting.value) return
  const parent = replyTo.value
  const now = Date.now()

  // 乐观更新：本地先插入 pending 条目；失败时移除并恢复草稿（§12 不静默丢失）。
  if (parent) {
    const thread = ensureThread(parent.id)
    thread.open = true
    thread.items.push({
      id: -now, body: text, handle: me.value?.handle || '我',
      created_at: new Date().toISOString(), user_id: me.value?.id ?? 0, pending: true,
    })
    if (!thread.loaded) await loadReplies(parent.id).catch(() => {})
  } else {
    items.value = [{
      id: -now, body: text, handle: me.value?.handle || '我',
      created_at: new Date().toISOString(), user_id: me.value?.id ?? 0,
      reply_count: 0, pending: true,
    }, ...items.value]
  }
  sentDraft.value = text
  draft.value = ''
  posting.value = true
  try {
    const created = await api<CommentItem>(`/api/v1/contents/${props.contentId}/comments`, {
      method: 'POST',
      body: { body: text, parent_id: parent?.id || undefined },
    })
    if (parent) {
      const thread = ensureThread(parent.id)
      const index = thread.items.findIndex((it) => it.id === -now)
      if (index >= 0) thread.items.splice(index, 1, { ...created, reply_to: parent.handle })
      bumpReplyCount(parent.id, 1)
    } else {
      const index = items.value.findIndex((it) => it.id === -now)
      if (index >= 0) items.value.splice(index, 1, created)
    }
    setTotal(total.value + 1)
    replyTo.value = null
    postFailed.value = false
    notice.value = ''
  } catch (error) {
    if (parent) {
      const thread = ensureThread(parent.id)
      thread.items = thread.items.filter((it) => it.id !== -now)
    } else {
      items.value = items.value.filter((it) => it.id !== -now)
    }
    draft.value = sentDraft.value
    postFailed.value = true
    notice.value = errorMessage(error)
  } finally {
    posting.value = false
  }
}

function bumpReplyCount(rootId: number, delta: number) {
  const parent = items.value.find((it) => it.id === rootId)
  if (parent) parent.reply_count = Math.max(0, parent.reply_count + delta)
}

// 删除自己的评论：两步确认（点一次变「确认删除」，3 秒后还原），替代弹窗避免抽屉内焦点嵌套。
function requestRemove(item: { id: number }) {
  if (confirmDelete.value === item.id) {
    confirmDelete.value = null
    void remove(item.id)
    return
  }
  confirmDelete.value = item.id
  if (confirmTimer) clearTimeout(confirmTimer)
  confirmTimer = setTimeout(() => { confirmDelete.value = null }, 3000)
}

async function remove(commentId: number) {
  try {
    await api(`/api/v1/contents/${props.contentId}/comments/${commentId}`, { method: 'DELETE' })
    for (const root of items.value) {
      const thread = threads[root.id]
      if (!thread) continue
      const index = thread.items.findIndex((it) => it.id === commentId)
      if (index >= 0) {
        thread.items.splice(index, 1)
        bumpReplyCount(root.id, -1)
      }
    }
    const before = items.value.length
    items.value = items.value.filter((it) => it.id !== commentId)
    if (items.value.length < before) setTotal(Math.max(0, total.value - 1))
  } catch (error) {
    notice.value = errorMessage(error)
  }
}

const MENTION = /^@[a-zA-Z0-9_]{3,32}$/

function tokens(body: string): { text: string; mention: boolean }[] {
  // split 带捕获组：奇数位是 @handle；纯文本不可能命中 mention 形态。
  return body
    .split(/(@[a-zA-Z0-9_]{3,32})/g)
    .filter((part) => part !== '')
    .map((part) => ({ text: part, mention: MENTION.test(part) }))
}

function when(value: string): string {
  const diff = Date.now() - new Date(value).getTime()
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时前`
  return `${Math.floor(minutes / 1440)} 天前`
}

function isMine(item: { user_id: number; id: number }): boolean {
  return !!me.value && item.user_id === me.value.id && item.id > 0
}
</script>

<template>
  <div v-if="props.open" class="sheet-layer">
    <button type="button" class="backdrop" aria-label="关闭评论" @click="emit('close')" />
    <section class="sheet" role="dialog" aria-modal="true" aria-label="评论">
      <div class="handle" aria-hidden="true" />
      <header class="head">
        <strong>{{ total }} 条评论</strong>
        <button type="button" aria-label="关闭评论" @click="emit('close')"><ImIcon name="close" :size="20" /></button>
      </header>
      <div class="list">
        <p v-if="notice" class="notice" role="status">{{ notice }}</p>
        <p v-if="!items.length && !loading" class="empty">还没有评论，来抢第一条。</p>
        <article v-for="comment in items" :key="comment.id" class="comment">
          <div class="avatar">{{ comment.handle.slice(0, 1) }}</div>
          <div class="body">
            <span class="name">{{ comment.handle }}</span>
            <p :class="{ pending: comment.pending }">
              <template v-for="(tk, i) in tokens(comment.body)" :key="i">
                <span v-if="tk.mention" class="mention">{{ tk.text }}</span>
                <template v-else>{{ tk.text }}</template>
              </template>
            </p>
            <div class="meta">
              <small>{{ when(comment.created_at) }}</small>
              <button type="button" class="action" @click="startReply(comment.handle, comment.id)">回复</button>
              <button
                v-if="isMine(comment)" type="button" class="action danger"
                @click="requestRemove(comment)"
              >{{ confirmDelete === comment.id ? '确认删除' : '删除' }}</button>
            </div>
            <div v-if="comment.reply_count > 0" class="thread">
              <button type="button" class="expand" @click="toggleThread(comment.id)">
                {{ threads[comment.id]?.open ? '收起回复' : `${comment.reply_count} 条回复` }}
              </button>
              <template v-if="threads[comment.id]?.open">
                <article v-for="reply in threads[comment.id]!.items" :key="reply.id" class="comment reply">
                  <div class="avatar small">{{ reply.handle.slice(0, 1) }}</div>
                  <div class="body">
                    <span class="name">{{ reply.handle }}</span>
                    <p :class="{ pending: reply.pending }">
                      <span v-if="reply.reply_to" class="reply-tag">回复 @{{ reply.reply_to }}</span>
                      <template v-for="(tk, i) in tokens(reply.body)" :key="i">
                        <span v-if="tk.mention" class="mention">{{ tk.text }}</span>
                        <template v-else>{{ tk.text }}</template>
                      </template>
                    </p>
                    <div class="meta">
                      <small>{{ when(reply.created_at) }}</small>
                      <button type="button" class="action" @click="startReply(reply.handle, reply.id)">回复</button>
                      <button
                        v-if="isMine(reply)" type="button" class="action danger"
                        @click="requestRemove(reply)"
                      >{{ confirmDelete === reply.id ? '确认删除' : '删除' }}</button>
                    </div>
                  </div>
                </article>
                <button
                  v-if="threads[comment.id]!.cursor !== null" type="button"
                  class="expand more" :disabled="threads[comment.id]!.loading"
                  @click="loadReplies(comment.id)"
                >{{ threads[comment.id]!.loading ? '加载中…' : '更多回复' }}</button>
              </template>
            </div>
          </div>
        </article>
        <button
          v-if="cursor !== null" type="button" class="expand more"
          :disabled="loading" @click="load(false)"
        >{{ loading ? '加载中…' : '加载更多评论' }}</button>
      </div>
      <form class="composer" :class="{ failed: postFailed }" @submit.prevent="submit">
        <span v-if="replyTo" class="reply-chip">
          回复 @{{ replyTo.handle }}
          <button type="button" aria-label="取消回复" @click="cancelReply"><ImIcon name="close" :size="12" /></button>
        </span>
        <div class="row">
          <input
            ref="input" v-model="draft" type="text" maxlength="200"
            :placeholder="replyTo ? `回复 @${replyTo.handle}…` : '说点什么…'"
            aria-label="评论内容"
          />
          <!-- @ 提及与表情：入口先就位，功能下一轮再做，因此保持禁用并标注「即将上线」，
               避免点了没反应被当成坏了。 -->
          <button
            type="button" class="pick" disabled
            title="@ 提及即将上线" aria-label="@ 提及（即将上线）"
          >
            <ImIcon name="at" :size="21" />
          </button>
          <button
            type="button" class="pick" disabled
            title="表情即将上线" aria-label="表情（即将上线）"
          >
            <ImIcon name="face" :size="22" />
          </button>
          <button type="submit" :disabled="!draft.trim() || posting" aria-label="发送评论">
            <ImIcon name="share" :size="18" />
          </button>
        </div>
        <p v-if="postFailed" class="failed-hint" role="status">发送失败，草稿已保留，可重试。</p>
      </form>
    </section>
  </div>
</template>

<style scoped>
.sheet-layer { position: fixed; inset: 0; z-index: var(--vp-im-z-sheet); }
.backdrop {
  position: absolute;
  inset: 0;
  border: 0;
  background: var(--vp-im-mask);
  cursor: pointer;
}
.sheet {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 66vh;
  display: flex;
  flex-direction: column;
  border-radius: var(--vp-radius-drawer) var(--vp-radius-drawer) 0 0;
  border-bottom: 0;
  /* 玻璃外壳但接近实色：避免下层操作栏透过抽屉干扰评论阅读（§4.2/§6.5）。 */
  background: rgba(20, 20, 22, 0.94);
}
.handle {
  width: 40px;
  height: 4px;
  margin: 10px auto 4px;
  border-radius: 2px;
  background: rgba(255, 255, 255, 0.35);
}
.head {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 8px 16px 12px;
  border-bottom: 1px solid var(--vp-im-glass-border);
  font-size: 14px;
}
.head button {
  position: absolute;
  right: 12px;
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border: 0;
  background: none;
  color: var(--vp-im-text-primary);
  cursor: pointer;
}
.list { flex: 1; overflow-y: auto; padding: 12px 16px; background: var(--vp-im-surface); }
.empty { margin: 28px 0; text-align: center; font-size: 13px; color: var(--vp-im-text-secondary); }
.notice { margin: 0 0 8px; font-size: 12px; color: var(--vp-im-accent); }
.comment { display: flex; gap: 10px; padding: 10px 0; }
.comment.reply { padding: 8px 0 0; }
.avatar {
  flex: none;
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: var(--vp-im-surface-raised);
  font-size: 13px;
}
.avatar.small { width: 26px; height: 26px; font-size: 11px; }
.body { flex: 1; min-width: 0; }
.name { font-size: 12px; color: var(--vp-im-text-secondary); }
.body p { margin: 3px 0; font-size: 14px; line-height: 1.5; word-break: break-word; }
.body p.pending { opacity: 0.55; }
.mention { color: var(--vp-im-accent); }
.reply-tag {
  margin-right: 6px;
  padding: 1px 6px;
  border-radius: 99px;
  font-size: 11px;
  background: rgba(255, 255, 255, 0.1);
  color: var(--vp-im-text-secondary);
}
.meta { display: flex; align-items: center; gap: 12px; }
.meta small { font-size: 11px; color: var(--vp-im-text-secondary); }
.action {
  border: 0;
  background: none;
  padding: 0;
  font-size: 11px;
  color: var(--vp-im-text-secondary);
  cursor: pointer;
}
.action.danger { color: #ff6b5e; }
.thread { margin-top: 2px; }
.expand {
  border: 0;
  background: none;
  padding: 4px 0;
  font-size: 12px;
  color: var(--vp-im-accent);
  cursor: pointer;
}
.expand.more { display: block; margin: 4px 0 0 36px; }
.expand:disabled { opacity: 0.5; cursor: not-allowed; }
.composer {
  padding: 8px 16px calc(10px + env(safe-area-inset-bottom));
  border-top: 1px solid var(--vp-im-glass-border);
  background: var(--vp-im-surface);
}
.composer .row { display: flex; gap: 8px; }
.composer.failed input { border-color: #ff6b5e; }
.failed-hint { margin: 6px 0 0; font-size: 11px; color: #ff6b5e; }
.reply-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 6px;
  padding: 3px 10px;
  border-radius: 99px;
  font-size: 11px;
  background: rgba(255, 255, 255, 0.1);
  color: var(--vp-im-text-secondary);
}
.reply-chip button {
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  border: 0;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.14);
  color: var(--vp-im-text-primary);
  cursor: pointer;
}
.composer input {
  flex: 1;
  min-width: 0;
  height: 42px;
  padding: 0 14px;
  border: 1px solid var(--vp-im-border);
  border-radius: 999px;
  background: var(--vp-im-surface-raised);
  color: var(--vp-im-text-primary);
  font-size: 13px;
  outline: none;
}
.composer input:focus { border-color: var(--vp-im-accent); }
.composer button[type="submit"] {
  width: 44px;
  height: 44px;
  border: 0;
  border-radius: 50%;
  background: var(--vp-im-accent);
  color: var(--vp-im-on-accent);
  cursor: pointer;
}
.composer button[type="submit"]:disabled { opacity: 0.45; cursor: not-allowed; }
/* 占位入口：可见但不可点，视觉上比输入框弱一档。 */
.composer .pick {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 32px;
  height: 42px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--vp-im-text-secondary);
  cursor: not-allowed;
  opacity: 0.72;
}
</style>
