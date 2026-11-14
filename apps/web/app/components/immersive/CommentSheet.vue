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
  display_name?: string
  avatar_url?: string
  /** 作者是已认证创作者时，昵称旁挂标签。 */
  verified_creator?: boolean
  created_at: string
  user_id: number
  reply_count: number
  pending?: boolean
}

interface ReplyItem {
  id: number
  body: string
  handle: string
  display_name?: string
  avatar_url?: string
  verified_creator?: boolean
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
// 待发送的 GIF：选图后先挂在输入框上方预览，发送时和文字一起提交（正文里存 [[gif:url]]）。
const pendingGif = ref('')
const postFailed = ref(false)
const notice = ref('')
const replyTo = ref<{ id: number; name: string } | null>(null)
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
  pendingGif.value = ''
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

// 没设置头像的用户回退到默认图标；再按 user_id 取一个固定色相，
// 让不同评论者的占位头像至少能区分开，而不是一排同样的灰圆。
function avatarTint(userId: number) {
  if (!userId) return undefined
  const hue = (userId * 47) % 360
  return { background: `hsl(${hue} 30% 26%)`, color: `hsl(${hue} 55% 78%)` }
}

// 展示用昵称：服务端已按「昵称优先、缺失回落夜街号」算好；
// 乐观插入的本地条目直接给「我」，等接口返回再换成真实昵称。
function authorName(item: { handle: string; display_name?: string }): string {
  return item.display_name?.trim() || item.handle
}

// 回复标识用昵称而不是 handle：handle 现在是 6 位夜街号，展示出来像一串 ID。
// 从 GIF 页选了一张图：挂进待发送位并收起面板，等用户点发送。
function onPickGif(url: string) {
  if (!url) return
  pendingGif.value = url
  emojiOpen.value = false
  input.value?.focus()
}

function startReply(target: { id: number; handle: string; display_name?: string }) {
  replyTo.value = { id: target.id, name: authorName(target) }
  input.value?.focus()
}

function cancelReply() {
  replyTo.value = null
}

async function submit() {
  const text = draft.value.trim()
  const gif = pendingGif.value
  if ((!text && !gif) || posting.value) return
  const body = [text, gif ? gifMarker(gif) : ''].filter(Boolean).join(' ')
  const parent = replyTo.value
  const now = Date.now()

  // 乐观更新：本地先插入 pending 条目；失败时移除并恢复草稿（§12 不静默丢失）。
  // 作者信息只写「我」，接口返回后整条替换成服务端的昵称与头像。
  if (parent) {
    const thread = ensureThread(parent.id)
    thread.open = true
    thread.items.push({
      id: -now, body, handle: '', display_name: '我',
      created_at: new Date().toISOString(), user_id: me.value?.id ?? 0, pending: true,
    })
    if (!thread.loaded) await loadReplies(parent.id).catch(() => {})
  } else {
    items.value = [{
      id: -now, body, handle: '', display_name: '我',
      created_at: new Date().toISOString(), user_id: me.value?.id ?? 0,
      reply_count: 0, pending: true,
    }, ...items.value]
  }
  sentDraft.value = text
  draft.value = ''
  emojiOpen.value = false
  posting.value = true
  try {
    const created = await api<CommentItem>(`/api/v1/contents/${props.contentId}/comments`, {
      method: 'POST',
      body: { body, parent_id: parent?.id || undefined },
    })
    if (parent) {
      const thread = ensureThread(parent.id)
      const index = thread.items.findIndex((it) => it.id === -now)
      if (index >= 0) thread.items.splice(index, 1, { ...created, reply_to: parent.name })
      bumpReplyCount(parent.id, 1)
    } else {
      const index = items.value.findIndex((it) => it.id === -now)
      if (index >= 0) items.value.splice(index, 1, created)
    }
    setTotal(total.value + 1)
    pendingGif.value = ''
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

const emojiOpen = ref(false)
const emojiAnchor = ref<HTMLElement | null>(null)

function toggleEmoji() {
  emojiOpen.value = !emojiOpen.value
  if (emojiOpen.value) nextTick(() => input.value?.focus())
}

// 插到光标处而不是末尾：用户可能先写了字、再回来补表情。
// 输入框 maxlength=200，直接赋值不受它约束，所以这里自己挡一道。
function insertEmoji(char: string) {
  const element = input.value
  if (draft.value.length + char.length > 200) return
  if (!element) {
    draft.value += char
    return
  }
  const start = element.selectionStart ?? draft.value.length
  const end = element.selectionEnd ?? start
  draft.value = draft.value.slice(0, start) + char + draft.value.slice(end)
  nextTick(() => {
    element.focus()
    const caret = start + char.length
    element.setSelectionRange(caret, caret)
  })
}

// 点面板以外的地方收起；面板与触发按钮都在 emojiAnchor 里。
function onDocPointerDown(event: PointerEvent) {
  if (!emojiOpen.value) return
  const target = event.target as Node | null
  if (target && emojiAnchor.value?.contains(target)) return
  emojiOpen.value = false
}
onMounted(() => document.addEventListener('pointerdown', onDocPointerDown))
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocPointerDown))

type TokenKind = 'text' | 'mention' | 'emoji' | 'gif'

interface Token {
  text: string
  kind: TokenKind
}

function gifMarker(url: string): string {
  return `[[gif:${url}]]`
}

function pushText(result: Token[], text: string) {
  // 普通片段再按表情切开：表情单独渲染成图片（public/emoji3d 下的 PNG），文字保持可选择。
  for (const piece of splitEmoji(text)) {
    result.push({ text: piece.value, kind: piece.type === 'emoji' ? 'emoji' : 'text' })
  }
}

function tokens(body: string): Token[] {
  const result: Token[] = []
  // 一段正文里可能同时有 @提及 和 GIF 标记：先按这两类切，再对纯文本片段切表情。
  const splitter = /(\[\[gif:https:\/\/[^\]\s]+\]\])|(@[a-zA-Z0-9_]{3,32})/g
  let cursor = 0
  for (const match of body.matchAll(splitter)) {
    const index = match.index ?? 0
    if (index > cursor) pushText(result, body.slice(cursor, index))
    const raw = match[0]
    if (raw.startsWith('[[gif:')) {
      result.push({ text: raw.slice(6, -2), kind: 'gif' })
    } else {
      result.push({ text: raw, kind: 'mention' })
    }
    cursor = index + raw.length
  }
  if (cursor < body.length) pushText(result, body.slice(cursor))
  return result
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
          <div class="avatar" :style="comment.avatar_url ? undefined : avatarTint(comment.user_id)">
            <img v-if="comment.avatar_url" :src="comment.avatar_url" alt="" loading="lazy">
            <ImIcon v-else name="user" :size="18" />
          </div>
          <div class="body">
            <span class="name">
              {{ authorName(comment) }}
              <CreatorBadge v-if="comment.verified_creator" class="name-badge" size="13" />
            </span>
            <p :class="{ pending: comment.pending }">
              <template v-for="(tk, i) in tokens(comment.body)" :key="i">
                <img
                  v-if="tk.kind === 'gif'" class="comment-gif"
                  :src="tk.text" alt="GIF" loading="lazy"
                >
                <span v-else-if="tk.kind === 'mention'" class="mention">{{ tk.text }}</span>
                <img
                  v-else-if="tk.kind === 'emoji' && emojiSrc(tk.text)" class="emoji"
                  :src="emojiSrc(tk.text)!" :alt="tk.text"
                />
                <template v-else>{{ tk.text }}</template>
              </template>
            </p>
            <div class="meta">
              <small>{{ when(comment.created_at) }}</small>
              <button type="button" class="action" @click="startReply(comment)">回复</button>
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
                  <div class="avatar small" :style="reply.avatar_url ? undefined : avatarTint(reply.user_id)">
                    <img v-if="reply.avatar_url" :src="reply.avatar_url" alt="" loading="lazy">
                    <ImIcon v-else name="user" :size="14" />
                  </div>
                  <div class="body">
                    <span class="name">
                      {{ authorName(reply) }}
                      <CreatorBadge v-if="reply.verified_creator" class="name-badge" size="12" />
                    </span>
                    <p :class="{ pending: reply.pending }">
                      <span v-if="reply.reply_to" class="reply-tag">回复 @{{ reply.reply_to }}</span>
                      <template v-for="(tk, i) in tokens(reply.body)" :key="i">
                        <img
                          v-if="tk.kind === 'gif'" class="comment-gif"
                          :src="tk.text" alt="GIF" loading="lazy"
                        >
                        <span v-else-if="tk.kind === 'mention'" class="mention">{{ tk.text }}</span>
                        <img
                          v-else-if="tk.kind === 'emoji' && emojiSrc(tk.text)" class="emoji"
                          :src="emojiSrc(tk.text)!" :alt="tk.text"
                        />
                        <template v-else>{{ tk.text }}</template>
                      </template>
                    </p>
                    <div class="meta">
                      <small>{{ when(reply.created_at) }}</small>
                      <button type="button" class="action" @click="startReply(reply)">回复</button>
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
          回复 @{{ replyTo.name }}
          <button type="button" aria-label="取消回复" @click="cancelReply"><ImIcon name="close" :size="12" /></button>
        </span>
        <div v-if="pendingGif" class="gif-preview">
          <img :src="pendingGif" alt="GIF 预览">
          <button type="button" aria-label="移除 GIF" @click="pendingGif = ''">
            <ImIcon name="close" :size="12" />
          </button>
        </div>
        <div class="row">
          <div class="input-wrap">
            <input
              ref="input" v-model="draft" type="text" maxlength="200"
              :placeholder="replyTo ? `回复 @${replyTo.name}…` : '说点什么…'"
              aria-label="评论内容"
            />
            <!-- @ 提及与表情内嵌在输入框右端；发送交给回车 / 软键盘「发送」键。 -->
            <div ref="emojiAnchor" class="pickers">
              <button
                type="button" class="pick" disabled
                title="@ 提及即将上线" aria-label="@ 提及（即将上线）"
              >
                <ImIcon name="at" :size="21" />
              </button>
              <button
                type="button" class="pick" :class="{ on: emojiOpen }"
                :aria-expanded="emojiOpen" aria-label="表情" title="表情" @click="toggleEmoji"
              >
                <ImIcon name="face" :size="22" />
              </button>
              <EmojiPanel v-if="emojiOpen" @pick="insertEmoji" @pick-gif="onPickGif" />
            </div>
          </div>
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
  /* 没有头像的用户回退到默认用户图标，图标色比正文弱一档。 */
  color: var(--vp-im-text-secondary);
  overflow: hidden;
}
.avatar img { display: block; width: 100%; height: 100%; object-fit: cover; }
.avatar.small { width: 26px; height: 26px; }
.body { flex: 1; min-width: 0; }
.name {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--vp-im-text-secondary);
}
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
  /* 移动端表情面板要以整条输入区为定位基准（见 EmojiPanel 的媒体查询）。 */
  position: relative;
  padding: 8px 16px calc(10px + env(safe-area-inset-bottom));
  border-top: 1px solid var(--vp-im-glass-border);
  background: var(--vp-im-surface);
}
.composer .row { display: flex; gap: 8px; }
.composer.failed .input-wrap { border-color: #ff6b5e; }
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
/* 一整条圆角输入区占满一行，@ / 表情内嵌在右端，不再有独立发送按钮。
   边框与底色挂在外壳上，input 自身透明，文字与图标才不会抢同一层。 */
.composer .input-wrap {
  display: flex;
  align-items: center;
  flex: 1 1 auto;
  min-width: 0;
  box-sizing: border-box;
  padding: 0 6px 0 14px;
  border: 1px solid var(--vp-im-border);
  border-radius: 999px;
  background: var(--vp-im-surface-raised);
}
.composer .input-wrap:focus-within { border-color: var(--vp-im-accent); }
.composer input {
  flex: 1 1 auto;
  min-width: 0;
  height: 40px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--vp-im-text-primary);
  font-size: 13px;
  outline: none;
}
/* 全局 :focus-visible 会给 input 画一圈品牌粉 box-shadow；input 现在直角无边框，
   那圈环会变成与外层胶囊不搭的矩形，像表单报错。焦点交给 .input-wrap 的边框表达。 */
.composer input:focus-visible { box-shadow: none; }
/* 表情面板以这里为定位锚点（面板向右对齐、浮在输入区上方）。
   窄屏时改成 static，让面板去以整条 .composer 为基准铺满一行，见 EmojiPanel。 */
.composer .pickers {
  position: relative;
  display: flex;
  align-items: center;
  flex: 0 0 auto;
}
@media (max-width: 560px) {
  .composer .pickers { position: static; }
}
.composer .pick {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 32px;
  height: 40px;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: none;
  color: var(--vp-im-text-secondary);
  cursor: pointer;
}
.composer .pick:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.1);
  color: var(--vp-im-text-primary);
}
.composer .pick.on { color: var(--vp-im-accent); }
/* @ 提及仍是占位：可见但不可点，视觉上比输入框弱一档。 */
.composer .pick:disabled { cursor: not-allowed; opacity: 0.72; }
/* 评论里的 GIF：限宽限高，不撑破气泡；这是外链图，加载失败时留一块底色而不是塌成 0 高。 */
.comment-gif {
  display: block;
  max-width: 168px;
  max-height: 168px;
  min-width: 80px;
  min-height: 80px;
  margin: 4px 0;
  border-radius: 10px;
  object-fit: cover;
  background: var(--vp-im-surface-raised);
}
/* 待发送的 GIF 预览：小图 + 移除按钮，与「回复 @」chip 同一层次。 */
.gif-preview {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 6px;
  padding: 3px 8px 3px 3px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.1);
}
.gif-preview img {
  width: 46px;
  height: 46px;
  border-radius: 9px;
  object-fit: cover;
  display: block;
}
.gif-preview button {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border: 0;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.14);
  color: var(--vp-im-text-primary);
  cursor: pointer;
}
/* 评论里的表情图：略高于正文、基线对齐，不撑高行高（3D 图比线性图标需要更大尺寸）。 */
.comment .emoji,
.comment .body .emoji {
  width: 20px;
  height: 20px;
  margin: 0 1px;
  vertical-align: -5px;
  display: inline-block;
}
</style>
