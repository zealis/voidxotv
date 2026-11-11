<script setup lang="ts">
// 图集发布（UI 规范 §8.1 / §5.4）：最多 18 张、可拖拽排序、首图为封面。
// 排序同时提供键盘可达的「左移 / 右移 / 设为封面」按钮，不只靠手势（§15）。
// 字段与视频模式共用，图片由服务端直写对象存储并登记 media_assets。
import { errorMessage } from '~/composables/useApi'

interface GalleryItem {
  id: number
  position: number
  url: string
  mime_type: string
  byte_size: number
  width?: number
  height?: number
}

type EditorStatus = 'uploading' | 'ready' | 'error'
interface EditorItem {
  key: string
  id: number | null
  url: string
  localUrl?: string
  file?: File
  status: EditorStatus
  progress: number
  error: string
  byteSize?: number
}

interface GalleryState {
  count: number
  ready: number
  busy: boolean
  valid: boolean
  coverUrl: string
}

const props = defineProps<{
  contentId: number | null
  ensureDraft: () => Promise<number | null>
  disabled?: boolean
}>()
const emit = defineEmits<{ change: [state: GalleryState] }>()

const { api, upload } = useApi()

const MAX = 18
const MIN = 2
const MAX_BYTES = 8 * 1024 * 1024
const ALLOWED = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/gif'])

const items = ref<EditorItem[]>([])
const hint = ref('')
const dragIndex = ref<number | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

const state = computed<GalleryState>(() => ({
  count: items.value.length,
  ready: items.value.filter((item) => item.status === 'ready').length,
  busy: items.value.some((item) => item.status === 'uploading'),
  valid: items.value.length >= MIN && items.value.length <= MAX
    && items.value.every((item) => item.status === 'ready'),
  coverUrl: items.value[0]?.url || '',
}))
watch(state, (value) => emit('change', value), { immediate: true, deep: true })

function uid(): string {
  return `img-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 ** 2).toFixed(1)} MB`
}

// 草稿恢复：父组件从 GET /contents/{id}/images 拿到列表后回填。
function hydrate(server: GalleryItem[]) {
  reset()
  items.value = server.map((item) => ({
    key: `srv-${item.id}`,
    id: item.id,
    url: item.url,
    status: 'ready' as EditorStatus,
    progress: 100,
    error: '',
    byteSize: item.byte_size,
  }))
}

function reset() {
  for (const item of items.value) {
    if (item.localUrl) URL.revokeObjectURL(item.localUrl)
  }
  items.value = []
  hint.value = ''
  dragIndex.value = null
}

function orderedIds(): number[] {
  return items.value
    .map((item) => item.id)
    .filter((id): id is number => id !== null)
}

defineExpose({ hydrate, reset, state, orderedIds })

// 串行上传：保证服务端 position 与用户选择顺序一致，也避免并发打满。
async function addFiles(list: FileList | File[] | null) {
  if (!list || props.disabled) return
  hint.value = ''
  const files = Array.from(list)
  const keys: string[] = []
  for (const file of files) {
    if (items.value.length + keys.length >= MAX) {
      hint.value = `图集最多 ${MAX} 张图片，多余的已忽略`
      break
    }
    if (!ALLOWED.has(file.type)) {
      hint.value = `${file.name} 不是支持的格式（仅 JPEG / PNG / WebP / GIF）`
      continue
    }
    if (file.size > MAX_BYTES || file.size <= 0) {
      hint.value = `${file.name} 超过 8 MiB 或为空文件`
      continue
    }
    const localUrl = URL.createObjectURL(file)
    const key = uid()
    items.value = [...items.value, {
      key, id: null, url: localUrl, localUrl, file,
      status: 'uploading', progress: 0, error: '',
    }]
    keys.push(key)
  }
  for (const key of keys) await uploadOne(key)
}

async function uploadOne(key: string) {
  // 必须取 ref 里的 proxy 对象，修改原始对象不会触发响应式更新。
  const entry = items.value.find((item) => item.key === key)
  if (!entry || !entry.file) return
  let contentId = props.contentId
  if (!contentId) contentId = await props.ensureDraft()
  if (!contentId) {
    entry.status = 'error'
    entry.error = '草稿创建失败，无法上传'
    return
  }
  const form = new FormData()
  form.append('image', entry.file)
  try {
    const result = await upload<{ item: GalleryItem }>(
      `/api/v1/contents/${contentId}/images`,
      form,
      (percent) => { entry.progress = percent },
    )
    entry.id = result.item.id
    entry.url = result.item.url
    entry.byteSize = result.item.byte_size
    entry.status = 'ready'
    entry.progress = 100
    entry.error = ''
    if (entry.localUrl) {
      URL.revokeObjectURL(entry.localUrl)
      entry.localUrl = undefined
    }
    if (!hint.value) hint.value = ''
  } catch (error) {
    entry.status = 'error'
    entry.error = errorMessage(error)
  }
}

async function retry(key: string) {
  const entry = items.value.find((item) => item.key === key)
  if (!entry) return
  entry.status = 'uploading'
  entry.progress = 0
  entry.error = ''
  await uploadOne(key)
}

async function remove(key: string) {
  if (props.disabled) return
  const index = items.value.findIndex((item) => item.key === key)
  if (index < 0) return
  const entry = items.value[index]
  if (!entry) return
  if (entry.id && props.contentId) {
    try {
      await api(`/api/v1/contents/${props.contentId}/images/${entry.id}`, { method: 'DELETE' })
    } catch (error) {
      hint.value = `删除失败：${errorMessage(error)}`
      return
    }
  }
  if (entry.localUrl) URL.revokeObjectURL(entry.localUrl)
  items.value.splice(index, 1)
}

function move(from: number, to: number) {
  if (props.disabled || from === to || to < 0 || to >= items.value.length) return
  const next = [...items.value]
  const moved = next.splice(from, 1)[0]
  if (!moved) return
  next.splice(to, 0, moved)
  items.value = next
  void persistOrder()
}

// 拖拽排序要求全部图片已上传：服务端按 id 集合做整批重排。
async function persistOrder() {
  if (state.value.busy || !items.value.every((item) => item.id)) {
    hint.value = '请等待全部图片上传完成后再排序'
    return
  }
  if (!props.contentId) return
  try {
    await api(`/api/v1/contents/${props.contentId}/images/order`, {
      method: 'PUT',
      body: { ids: items.value.map((item) => item.id as number) },
    })
    hint.value = ''
  } catch (error) {
    hint.value = `排序保存失败：${errorMessage(error)}`
  }
}

function onDragStart(index: number, event: DragEvent) {
  if (props.disabled) return
  dragIndex.value = index
  event.dataTransfer?.setData('text/plain', String(index))
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function onDropCard(index: number, event: DragEvent) {
  event.preventDefault()
  const raw = dragIndex.value ?? Number(event.dataTransfer?.getData('text/plain'))
  dragIndex.value = null
  if (Number.isInteger(raw) && raw >= 0 && raw < items.value.length) move(raw, index)
}

function onDropZone(event: DragEvent) {
  event.preventDefault()
  if (event.dataTransfer?.files?.length) void addFiles(event.dataTransfer.files)
}

function onInputChange(event: Event) {
  const input = event.target as HTMLInputElement
  void addFiles(input.files)
  input.value = ''
}
</script>

<template>
  <div class="gallery-editor">
    <div
      class="drop"
      :class="{ disabled: !!disabled }"
      @dragover.prevent
      @drop="onDropZone"
    >
      <input
        ref="fileInput" type="file" accept="image/jpeg,image/png,image/webp,image/gif"
        multiple :disabled="disabled" @change="onInputChange"
      />
      <span class="drop-icon" aria-hidden="true">＋</span>
      <strong>点击选择或拖入图片</strong>
      <small>最多 {{ MAX }} 张 · 单张 ≤ 8 MiB · 首张为封面 · 可拖拽排序</small>
    </div>

    <div class="meta">
      <span>{{ items.length }}/{{ MAX }} 张</span>
      <span v-if="items.length && items.length < MIN" class="warn">还需 {{ MIN - items.length }} 张才能发布</span>
      <span v-else-if="state.busy" class="muted">上传中…</span>
      <span v-else-if="state.valid" class="ok">已就绪，首图为封面</span>
    </div>

    <ul v-if="items.length" class="grid" aria-label="图集图片与顺序">
      <li
        v-for="(item, index) in items" :key="item.key"
        class="card"
        :class="{ cover: index === 0, failed: item.status === 'error' }"
        :draggable="!disabled"
        @dragstart="onDragStart(index, $event)"
        @dragover.prevent
        @drop="onDropCard(index, $event)"
      >
        <div class="thumb">
          <img :src="item.url" :alt="`第 ${index + 1} 张图片`" />
          <span v-if="index === 0" class="cover-badge">封面</span>
          <span class="order">{{ index + 1 }}</span>
          <div v-if="item.status === 'uploading'" class="progress">
            <div :style="{ width: `${item.progress}%` }" />
          </div>
        </div>
        <div class="card-status">
          <template v-if="item.status === 'uploading'">{{ item.progress }}% 上传中</template>
          <template v-else-if="item.status === 'error'">
            <span class="err-text" :title="item.error">上传失败</span>
            <button type="button" :disabled="disabled" @click="retry(item.key)">重试</button>
          </template>
          <template v-else>已上传 · {{ formatBytes(item.byteSize || 0) }}</template>
        </div>
        <div class="card-actions">
          <button
            type="button" :disabled="disabled || index === 0"
            :aria-label="`第 ${index + 1} 张设为封面`" title="设为封面"
            @click="move(index, 0)"
          >⤒</button>
          <button
            type="button" :disabled="disabled || index === 0"
            :aria-label="`第 ${index + 1} 张左移`" title="左移"
            @click="move(index, index - 1)"
          >←</button>
          <button
            type="button" :disabled="disabled || index === items.length - 1"
            :aria-label="`第 ${index + 1} 张右移`" title="右移"
            @click="move(index, index + 1)"
          >→</button>
          <button
            type="button" class="danger" :disabled="disabled"
            :aria-label="`删除第 ${index + 1} 张`" title="删除"
            @click="remove(item.key)"
          >×</button>
        </div>
        <p v-if="item.status === 'error'" class="err-detail">{{ item.error }}</p>
      </li>
    </ul>

    <p v-if="hint" class="hint" role="status">{{ hint }}</p>
    <p class="note">
      图片顺序即沉浸流内左右横滑的顺序；除拖拽外也可用卡片上的左移 / 右移 / 设为封面按钮调整。
    </p>
  </div>
</template>

<style scoped>
.gallery-editor { display: grid; gap: 10px; }
.drop {
  position: relative; display: grid; place-items: center; gap: 6px;
  padding: 22px 14px; text-align: center; cursor: pointer;
  border: 1px dashed var(--vp-tool-border-strong); border-radius: 12px;
  background: var(--vp-tool-input);
}
.drop.disabled { opacity: .55; cursor: not-allowed; }
.drop input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
.drop-icon { font-size: 24px; color: var(--vp-tool-accent-ink); }
.drop strong { font-size: 13px; font-weight: 500; color: var(--vp-tool-text); }
.drop small { font-size: 11px; color: var(--vp-tool-text-2); }
.meta { display: flex; gap: 12px; align-items: center; font-size: 11px; color: var(--vp-tool-text-2); }
.meta .warn { color: var(--vp-tool-danger-ink); }
.meta .ok { color: var(--vp-tool-positive); }
.meta .muted { opacity: .8; }
.grid {
  list-style: none; margin: 0; padding: 0; display: grid; gap: 10px;
  grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
}
.card {
  padding: 6px; border-radius: 10px; cursor: grab;
  border: 1px solid var(--vp-tool-border); background: var(--vp-tool-surface);
}
.card.cover { border-color: var(--vp-tool-accent-ink); box-shadow: 0 0 0 1px var(--vp-tool-focus); }
.card.failed { border-color: var(--vp-tool-danger-border); }
.thumb { position: relative; aspect-ratio: 3 / 4; border-radius: 7px; overflow: hidden; background: var(--vp-tool-bg); }
.thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
.cover-badge {
  position: absolute; left: 5px; top: 5px; padding: 2px 6px; border-radius: 5px;
  font-size: 10px; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent);
}
.order {
  position: absolute; right: 5px; top: 5px; min-width: 18px; text-align: center;
  padding: 2px 4px; border-radius: 5px; font-size: 10px;
  background: var(--vp-tool-overlay); color: var(--vp-tool-text);
}
.progress { position: absolute; left: 0; right: 0; bottom: 0; height: 4px; background: var(--vp-tool-overlay); }
.progress div { height: 100%; background: var(--vp-tool-accent); transition: width .15s ease; }
.card-status {
  display: flex; align-items: center; justify-content: center; gap: 6px;
  min-height: 20px; margin-top: 5px; font-size: 10px; color: var(--vp-tool-text-2);
}
.card-status button { padding: 1px 6px; font-size: 10px; border-radius: 5px; cursor: pointer;
  border: 1px solid var(--vp-tool-border-strong); background: transparent; color: var(--vp-tool-text); }
.err-text { color: var(--vp-tool-danger-ink); }
.card-actions { display: flex; justify-content: center; gap: 3px; margin-top: 4px; }
.card-actions button {
  width: 24px; height: 22px; line-height: 1; border-radius: 5px; cursor: pointer; font-size: 12px;
  border: 1px solid var(--vp-tool-border); background: var(--vp-tool-surface-2); color: var(--vp-tool-text);
}
.card-actions button:disabled { opacity: .35; cursor: not-allowed; }
.card-actions .danger { color: var(--vp-tool-danger-ink); border-color: var(--vp-tool-danger-border); }
.err-detail { margin: 4px 0 0; font-size: 10px; line-height: 1.4; color: var(--vp-tool-danger-ink); word-break: break-all; }
.hint { margin: 0; font-size: 11px; color: var(--vp-tool-danger-ink); }
.note { margin: 0; font-size: 11px; line-height: 1.6; color: var(--vp-tool-text-2); }
</style>
