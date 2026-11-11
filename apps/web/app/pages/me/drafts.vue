<script setup lang="ts">
// 「我 · 草稿箱」（技术规范 §6.1）：继续编辑或删除自动保存的草稿。
// 草稿不进入任何公开查询与统计；删除是软删除，保留统计与申诉引用。
import { errorMessage } from '~/composables/useApi'

useSeoMeta({ title: '草稿箱 · 夜街', robots: 'noindex,nofollow' })

interface Draft {
  id: number
  slug: string
  title: string
  description: string
  visibility: string
  publish_state: string
  scheduled_at?: string
  poi?: { name?: string; address?: string }
  created_at: string
  updated_at: string
}

const PAGE_SIZE = 20

const { api, getMe } = useApi()
const items = ref<Draft[]>([])
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')
const confirming = ref<Draft | null>(null)
const deleting = ref(false)
// 分页与批量删除（§11.1 列表型页面的批量操作）。
const hasMore = ref(false)
const loadingMore = ref(false)
const selected = ref<Set<number>>(new Set())
const batchConfirming = ref(false)
const batchDeleting = ref(false)
const asyncState = useAsyncState({ keepOnError: true })
const { phase, errorText, partial, retrying } = asyncState

const visibilityText: Record<string, string> = {
  public: '所有人可见', friends: '好友可见', private: '仅自己',
}

function when(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('zh-CN', {
    hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  })
}

// reset=true 重新拉第一页；否则用当前条数作为 offset 追加下一页。
// 排序方向：desc = 最新创建在前（默认），asc = 最早创建在前。
const sortOrder = ref<'desc' | 'asc'>('desc')

function toggleSort() {
  sortOrder.value = sortOrder.value === 'desc' ? 'asc' : 'desc'
  void load(true)
}

async function load(reset = true) {
  checking.value = false
  if (reset) asyncState.begin()
  else loadingMore.value = true
  try {
    const offset = reset ? 0 : items.value.length
    const data = await api<{ items: Draft[]; has_more?: boolean }>(
      `/api/v1/contents/mine?limit=${PAGE_SIZE}&offset=${offset}&state=draft&order=${sortOrder.value}`,
    )
    const page = (data.items || []).filter((item) => item.publish_state === 'draft')
    items.value = reset ? page : [...items.value, ...page]
    hasMore.value = !!data.has_more
    if (reset) {
      selected.value = new Set()
      asyncState.succeed(items.value.length === 0)
    }
  } catch (error) {
    if (reset) asyncState.fail(error)
    else message.value = errorMessage(error)
  } finally {
    loadingMore.value = false
  }
}

async function remove() {
  const target = confirming.value
  if (!target || deleting.value) return
  deleting.value = true
  message.value = ''
  try {
    await api(`/api/v1/contents/${target.id}`, { method: 'DELETE' })
    items.value = items.value.filter((item) => item.id !== target.id)
    if (selected.value.has(target.id)) {
      const next = new Set(selected.value)
      next.delete(target.id)
      selected.value = next
    }
    confirming.value = null
    if (!items.value.length) asyncState.succeed(true)
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    deleting.value = false
  }
}

const allSelected = computed(() =>
  items.value.length > 0 && items.value.every((item) => selected.value.has(item.id)))

function toggle(id: number) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}

function toggleAll() {
  selected.value = allSelected.value
    ? new Set()
    : new Set(items.value.map((item) => item.id))
}

// 批量删除允许部分成功：不存在或不属于当前账号的 id 会原样回传，
// 界面上保留这些项并提示，而不是整批失败。
async function removeSelected() {
  const ids = [...selected.value]
  if (!ids.length || batchDeleting.value) return
  batchDeleting.value = true
  message.value = ''
  try {
    const result = await api<{ deleted: number; missing: number[] }>(
      '/api/v1/contents/batch-delete', { method: 'POST', body: { ids } },
    )
    const missing = new Set(result.missing || [])
    const removed = new Set(ids.filter((id) => !missing.has(id)))
    items.value = items.value.filter((item) => !removed.has(item.id))
    selected.value = new Set(result.missing || [])
    batchConfirming.value = false
    if (missing.size) message.value = `${missing.size} 条已不存在或不属于你，未删除`
    if (!items.value.length) asyncState.succeed(true)
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    batchDeleting.value = false
  }
}

onMounted(async () => {
  const me = await getMe()
  if (!me) {
    checking.value = false
    needLogin.value = true
    return
  }
  await load()
})
</script>

<template>
  <!-- 与「我」的其他子页（关系 / 个人库）统一：沉浸式深色基线。 -->
  <main class="drafts" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/me" class="back" aria-label="返回我的页面">←</NuxtLink>
      <h1>草稿箱</h1>
      <NuxtLink to="/publish?new=1" class="link primary">新建作品</NuxtLink>
    </header>

    <p class="intro">
      编辑中的作品会自动保存为草稿，发布前不会出现在公开页面或统计中。
      <button type="button" class="sort" @click="toggleSort">
        {{ sortOrder === 'desc' ? '最新创建在前' : '最早创建在前' }}
      </button>
    </p>
    <p v-if="message" class="notice" role="alert">{{ message }}</p>
    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后查看并继续编辑草稿。</p>
      <NuxtLink to="/login?redirect=%2Fme%2Fdrafts" class="cta">登录</NuxtLink>
    </section>

    <VpAsyncState
      v-else :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="3" empty-title="还没有草稿"
      empty-hint="去发布页写点什么，内容会自动保存到这里。"
      @retry="load()"
    >
      <template #empty>
        <NuxtLink to="/publish?new=1" class="empty-cta">去发布</NuxtLink>
      </template>
      <div class="batch-bar">
        <label class="check-all">
          <input type="checkbox" :checked="allSelected" @change="toggleAll" />
          全选
        </label>
        <span v-if="selected.size" class="selected-count">已选 {{ selected.size }} 项</span>
        <button
          v-if="selected.size" type="button" class="btn danger batch-delete"
          @click="batchConfirming = true"
        >删除所选</button>
      </div>

      <ul class="list">
        <li v-for="item in items" :key="item.id" :class="{ picked: selected.has(item.id) }">
          <label class="pick">
            <input
              type="checkbox" :checked="selected.has(item.id)"
              :aria-label="`选择草稿 ${item.title || '无标题草稿'}`"
              @change="toggle(item.id)"
            />
          </label>
          <div class="main">
            <div class="meta">
              <strong class="title">{{ item.title || '（无标题草稿）' }}</strong>
              <span class="sub">
                最后编辑 {{ when(item.updated_at) }} ·
                {{ visibilityText[item.visibility] || item.visibility }}
                <template v-if="item.scheduled_at"> · 定时 {{ when(item.scheduled_at) }}</template>
                <template v-if="item.poi?.name"> · {{ item.poi.name }}</template>
              </span>
            </div>
            <span class="badge">草稿</span>
          </div>
          <p v-if="item.description" class="desc">{{ item.description }}</p>
          <div class="actions">
            <NuxtLink class="btn edit" :to="`/publish?draft=${item.id}`">继续编辑</NuxtLink>
            <button type="button" class="btn danger" @click="confirming = item">删除</button>
          </div>
        </li>
      </ul>
      <div v-if="hasMore" class="more-wrap">
        <button type="button" class="btn" :disabled="loadingMore" @click="load(false)">
          {{ loadingMore ? '加载中…' : '加载更多草稿' }}
        </button>
      </div>
    </VpAsyncState>

    <VpDialog
      :open="!!confirming" title="删除草稿" :busy="deleting"
      @close="confirming = null"
    >
      <p class="dialog-text">
        确定删除「{{ confirming?.title || '（无标题草稿）' }}」吗？删除后不会出现在公开页面，也无法继续编辑。
      </p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="confirming = null">取消</button>
        <button type="button" class="btn danger" :disabled="deleting" @click="remove">
          {{ deleting ? '删除中…' : '确认删除' }}
        </button>
      </div>
    </VpDialog>

    <VpDialog
      :open="batchConfirming" title="批量删除草稿" :busy="batchDeleting"
      @close="batchConfirming = false"
    >
      <p class="dialog-text">
        确定删除选中的 {{ selected.size }} 条草稿吗？删除后不会出现在公开页面，也无法继续编辑。
      </p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="batchConfirming = false">取消</button>
        <button type="button" class="btn danger" :disabled="batchDeleting" @click="removeSelected">
          {{ batchDeleting ? '删除中…' : `确认删除 ${selected.size} 条` }}
        </button>
      </div>
    </VpDialog>
  </main>
</template>

<style scoped>
.drafts {
  min-height: 100dvh; padding: 20px 20px 56px; box-sizing: border-box;
  background: var(--vp-im-bg); color: var(--vp-im-text-primary);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: 820px; margin: 0 auto 14px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center; border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.link {
  margin-left: auto; padding: 7px 14px; border-radius: 99px; font-size: 12px;
  text-decoration: none; white-space: nowrap;
  border: 1px solid transparent;
  background: var(--vp-im-accent); color: var(--vp-im-on-accent); font-weight: 600;
}
/* 说明文字与排序开关同一行；排序用胶囊，不再用浏览器默认按钮样式。 */
.intro {
  max-width: 820px; margin: 0 auto 16px;
  display: flex; align-items: center; flex-wrap: wrap; gap: 8px;
  font-size: 12px; line-height: 1.7; color: var(--vp-im-text-secondary);
}
.sort {
  padding: 5px 12px; border: 0; border-radius: 99px; cursor: pointer;
  background: rgba(255, 255, 255, 0.06); color: var(--vp-im-text-secondary);
  font-size: 12px; font-family: inherit;
}
.sort:hover, .sort:focus-visible { background: rgba(255, 255, 255, 0.1); color: var(--vp-im-text-primary); }
.empty-cta { color: var(--vp-im-accent); font-size: 12px; text-decoration: none; }
.empty-cta:hover { text-decoration: underline; }
/* VpAsyncState 自身不限宽，空态卡会撑满整屏；跟列表对齐到同一栏。 */
.drafts :deep(.vp-state) {
  max-width: 820px; margin-left: auto; margin-right: auto;
  box-sizing: border-box;
}
.hint, .gate { max-width: 820px; margin: 40px auto; text-align: center; font-size: 13px; color: var(--vp-im-text-secondary); }
.gate .cta {
  display: inline-block; margin-top: 12px; padding: 9px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: var(--vp-im-on-accent);
  text-decoration: none; font-size: 13px;
}
.notice {
  max-width: 820px; margin: 0 auto 12px; padding: 10px 12px; border-radius: 10px;
  background: rgba(255, 105, 97, 0.14); color: #ffb4ae; font-size: 12px;
}
.list { list-style: none; margin: 0 auto; padding: 0; max-width: 820px; display: grid; gap: 10px; }
.batch-bar {
  max-width: 820px; margin: 0 auto 10px; display: flex; align-items: center; gap: 12px;
  font-size: 12px; color: var(--vp-im-text-secondary);
}
.check-all { display: inline-flex; align-items: center; gap: 7px; cursor: pointer; }
/* 原生 checkbox 在深色底上是白方块，改成跟主题一致的小方框（选中为品牌粉）。 */
.check-all input, .pick input {
  appearance: none;
  -webkit-appearance: none;
  display: inline-grid;
  place-items: center;
  width: 16px;
  height: 16px;
  margin: 0;
  border: 1px solid rgba(255, 255, 255, 0.28);
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.06);
  cursor: pointer;
}
.check-all input:checked, .pick input:checked {
  background: var(--vp-im-accent);
  border-color: var(--vp-im-accent);
}
.check-all input:checked::after, .pick input:checked::after {
  content: '';
  width: 8px;
  height: 4px;
  border-left: 2px solid #fff;
  border-bottom: 2px solid #fff;
  transform: rotate(-45deg) translateY(-1px);
}
.selected-count { color: var(--vp-im-text-primary); }
.batch-delete { margin-left: auto; }
.list li {
  position: relative; padding: 14px 16px 14px 44px; border-radius: 14px;
  background: rgba(255, 255, 255, 0.04); border: 1px solid rgba(255, 255, 255, 0.06);
}
.list li.picked { border-color: var(--vp-im-accent); }
.pick { position: absolute; left: 15px; top: 16px; display: flex; }
.more-wrap { max-width: 820px; margin: 14px auto 0; text-align: center; }
.main { display: flex; align-items: center; gap: 10px; }
.meta { flex: 1; min-width: 0; display: grid; gap: 5px; }
.title { font-size: 14px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sub { font-size: 11px; color: var(--vp-im-text-secondary); line-height: 1.6; }
.badge {
  flex: none; padding: 2px 8px; border-radius: 4px; font-size: 10px;
  border: 1px solid rgba(255, 255, 255, 0.16); color: var(--vp-im-text-secondary);
}
.desc {
  margin: 10px 0 0; font-size: 12px; line-height: 1.6; color: var(--vp-im-text-secondary);
  overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical;
}
.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 12px; }
.btn {
  padding: 6px 14px; border-radius: 99px; cursor: pointer; font-size: 12px;
  font-family: inherit; text-decoration: none;
  border: 1px solid rgba(255, 255, 255, 0.16);
  background: transparent; color: var(--vp-im-text-primary);
}
.btn.edit { border-color: rgba(255, 255, 255, 0.16); }
.btn.danger { border-color: rgba(255, 105, 97, 0.6); color: #ffb4ae; }
.btn:disabled { opacity: .6; cursor: not-allowed; }
.dialog-text { margin: 0 0 14px; font-size: 13px; line-height: 1.7; color: var(--vp-im-text-secondary); }
.dialog-actions { display: flex; justify-content: flex-end; gap: 10px; }
@media (max-width: 560px) {
  .drafts { padding: 16px 14px 48px; }
  .main { flex-wrap: wrap; }
  .meta { flex: 1 1 calc(100% - 60px); }
}
</style>
