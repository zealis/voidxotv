<script setup lang="ts">
// 创作者中心 · 作品管理（§11.1）：改可见性、删除。删除是软删除，
// 作品从公开页面与列表消失，但统计与申诉记录保留引用。
import { errorMessage } from '~/composables/useApi'

interface Work {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  description: string
  visibility: 'public' | 'friends' | 'private'
  publish_state: string
  review_status: string
  created_at: string
}

// 图集图片：顺序即展示顺序，首图就是封面。
interface GalleryImage {
  id: number
  position: number
  url: string
  mime_type: string
  byte_size: number
}

const { api, upload } = useApi()
const asyncState = useAsyncState({
  keepOnError: true,
  isEmpty: () => items.value.length === 0,
})
const { phase, errorText, partial, retrying } = asyncState

const items = ref<Work[]>([])
const loading = ref(true)
const message = ref('')
const saved = ref('')
const galleryImages = ref<GalleryImage[]>([])
const galleryBusy = ref(false)
const busy = ref<Record<number, boolean>>({})
const confirming = ref<number | null>(null)
const edit = reactive({
  open: false, id: 0,
  type: 'video' as 'video' | 'image',
  title: '', description: '', error: '', saving: false,
  // §8.1 封面：视频作品可在发布后换封面（图集以首图为封面）。
  coverUrl: '', coverPreview: '', coverFile: null as File | null, coverBusy: false,
})
let savedTimer: ReturnType<typeof setTimeout> | null = null

const visibilityOptions = [
  { value: 'public', label: '公开' },
  { value: 'friends', label: '好友' },
  { value: 'private', label: '私密' },
]

const stateText: Record<string, string> = {
  draft: '草稿', scheduled: '定时发布', published: '已发布',
}
const reviewText: Record<string, string> = {
  pending: '审核中', approved: '已通过', rejected: '未通过',
  taken_down: '已下架', limited: '已限流',
}

function statusOf(work: Work): { text: string; tone: string } {
  if (work.publish_state === 'draft') return { text: '草稿', tone: 'neutral' }
  if (work.publish_state === 'scheduled') return { text: '定时发布', tone: 'neutral' }
  const text = reviewText[work.review_status] || stateText[work.publish_state] || work.publish_state
  const tone = work.review_status === 'approved' ? 'positive'
    : work.review_status === 'rejected' || work.review_status === 'taken_down' ? 'danger'
      : 'attention'
  return { text, tone }
}

function flash(text: string) {
  saved.value = text
  if (savedTimer) clearTimeout(savedTimer)
  savedTimer = setTimeout(() => { saved.value = '' }, 1800)
}

async function load() {
  loading.value = true
  message.value = ''
  await asyncState.run(async () => {
    const data = await api<{ items: Work[] }>('/api/v1/contents/mine?limit=50')
    items.value = data.items || []
    return data
  })
  loading.value = false
}

async function changeVisibility(work: Work, value: string | number) {
  const next = String(value)
  if (next === work.visibility || busy.value[work.id]) return
  busy.value = { ...busy.value, [work.id]: true }
  message.value = ''
  try {
    const updated = await api<Work>(`/api/v1/contents/${work.id}/manage`, {
      method: 'PATCH', body: { visibility: next },
    })
    work.visibility = updated.visibility
    flash('可见性已更新')
  } catch (error) {
    message.value = errorMessage(error)
    await load()
  } finally {
    busy.value = { ...busy.value, [work.id]: false }
  }
}

const COVER_TYPES = ['image/png', 'image/jpeg', 'image/webp']
// 图集新增图片的类型口径与后端 allowedImageTypes 一致（多一个 GIF）。
const GALLERY_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']
const COVER_MAX_BYTES = 8 * 1024 * 1024

function clearCoverPick() {
  if (edit.coverPreview) URL.revokeObjectURL(edit.coverPreview)
  edit.coverPreview = ''
  edit.coverFile = null
}

function closeEdit() {
  edit.open = false
  clearCoverPick()
}

async function openEdit(work: Work) {
  clearCoverPick()
  edit.open = true
  edit.id = work.id
  edit.type = work.type
  edit.title = work.title || ''
  edit.description = work.description || ''
  edit.error = ''
  edit.coverUrl = ''
  galleryImages.value = []
  galleryBusy.value = false
  if (work.type !== 'video') {
    // 图集：首图就是封面，可直接调整顺序（已发布作品会顺带触发页面失效）。
    try {
      const data = await api<{ items: GalleryImage[] }>(`/api/v1/contents/${work.id}/images`)
      if (edit.id === work.id) galleryImages.value = data.items || []
    } catch { /* 读不到图片列表不影响改标题与简介 */ }
    return
  }
  // 当前封面从转码产物清单读：无产物时接口返回空串，不当作错误。
  try {
    const data = await api<{ url: string }>(`/api/v1/contents/${work.id}/cover`)
    if (edit.id === work.id) edit.coverUrl = data.url || ''
  } catch { /* 读不到封面不影响改标题与简介 */ }
}

// 增删与全量拖拽排序（已发布作品同样可用）：全部走既有 images 端点，
// 服务端在已发布状态下会写 content.updated 触发页面再验证。
async function addGalleryImage(event: Event) {
  const inputEl = event.target as HTMLInputElement
  const file = inputEl.files?.[0]
  inputEl.value = ''
  if (!file || galleryBusy.value || !edit.id) return
  if (!GALLERY_TYPES.includes(file.type) || file.size > COVER_MAX_BYTES) {
    edit.error = '仅支持 PNG / JPEG / WebP / GIF，且不超过 8 MiB'
    return
  }
  galleryBusy.value = true
  edit.error = ''
  try {
    const form = new FormData()
    form.append('image', file, file.name || 'image.jpg')
    const data = await upload<{ items: GalleryImage[] }>(
      `/api/v1/contents/${edit.id}/images`, form,
    )
    galleryImages.value = data.items || galleryImages.value
    flash('图片已添加')
  } catch (error) {
    edit.error = errorMessage(error)
  } finally {
    galleryBusy.value = false
  }
}

async function removeGalleryImage(assetID: number) {
  if (galleryBusy.value || galleryImages.value.length <= 2 || !edit.id) return
  galleryBusy.value = true
  edit.error = ''
  try {
    const data = await api<{ items: GalleryImage[] }>(
      `/api/v1/contents/${edit.id}/images/${assetID}`, { method: 'DELETE' },
    )
    galleryImages.value = data.items || []
    flash('图片已删除')
  } catch (error) {
    edit.error = errorMessage(error)
  } finally {
    galleryBusy.value = false
  }
}

const galleryDragIndex = ref<number | null>(null)

function galleryDragStart(index: number, event: DragEvent) {
  if (galleryBusy.value) return
  galleryDragIndex.value = index
  event.dataTransfer?.setData('text/plain', String(index))
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

async function galleryDrop(index: number, event: DragEvent) {
  event.preventDefault()
  const raw = galleryDragIndex.value ?? Number(event.dataTransfer?.getData('text/plain'))
  galleryDragIndex.value = null
  if (!Number.isInteger(raw) || raw === index || galleryBusy.value || !edit.id) return
  const current = galleryImages.value
  if (raw < 0 || raw >= current.length) return
  const next = [...current]
  const [moved] = next.splice(raw, 1)
  next.splice(index, 0, moved!)
  // 乐观更新后按新顺序提交；首图变化即封面变化。
  galleryImages.value = next
  try {
    const data = await api<{ items: GalleryImage[] }>(`/api/v1/contents/${edit.id}/images/order`, {
      method: 'PUT', body: { ids: next.map((item) => item.id) },
    })
    galleryImages.value = data.items || next
    flash('顺序已更新')
  } catch (error) {
    galleryImages.value = current
    edit.error = errorMessage(error)
  }
}

// 把某张图提到首位 = 换图集封面。已发布作品的排序接口会写 content.updated，
// 让预渲染页与 CDN 一起失效；草稿则直接生效。
async function setGalleryCover(assetID: number) {
  const current = galleryImages.value
  if (galleryBusy.value || !current.length || current[0]?.id === assetID) return
  galleryBusy.value = true
  edit.error = ''
  const ids = [assetID, ...current.filter((item) => item.id !== assetID).map((item) => item.id)]
  try {
    const data = await api<{ items: GalleryImage[] }>(`/api/v1/contents/${edit.id}/images/order`, {
      method: 'PUT', body: { ids },
    })
    galleryImages.value = data.items || galleryImages.value
    edit.coverUrl = galleryImages.value[0]?.url || ''
    flash('封面已更新')
  } catch (error) {
    edit.error = errorMessage(error)
  } finally {
    galleryBusy.value = false
  }
}

function onCoverPick(event: Event) {
  const input = event.target as HTMLInputElement
  const picked = input.files?.[0]
  // 清空 value，允许连续选择同一个文件。
  input.value = ''
  if (!picked) return
  edit.error = ''
  if (!COVER_TYPES.includes(picked.type)) {
    edit.error = '封面仅支持 PNG / JPEG / WebP'
    return
  }
  if (picked.size > COVER_MAX_BYTES) {
    edit.error = '封面图片不能超过 8 MiB'
    return
  }
  clearCoverPick()
  edit.coverFile = picked
  edit.coverPreview = URL.createObjectURL(picked)
}

async function saveEdit() {
  const title = edit.title.trim()
  if (title.length > 30) { edit.error = '标题不能超过 30 个字符'; return }
  if (edit.description.length > 200) { edit.error = '简介不能超过 200 个字符'; return }
  edit.error = ''
  // 先换封面再存文本：封面失败时保留对话框，避免「文本改了、封面没改」的错觉。
  if (edit.coverFile) {
    edit.coverBusy = true
    try {
      const form = new FormData()
      form.append('image', edit.coverFile, edit.coverFile.name || 'cover.jpg')
      const result = await upload<{ key: string; url: string }>(
        `/api/v1/contents/${edit.id}/cover`, form,
      )
      edit.coverUrl = result?.url || ''
      clearCoverPick()
    } catch (error) {
      edit.error = `封面未能保存：${errorMessage(error)}`
      return
    } finally {
      edit.coverBusy = false
    }
  }
  edit.saving = true
  try {
    const updated = await api<Work>(`/api/v1/contents/${edit.id}/manage`, {
      method: 'PATCH', body: { title, description: edit.description.trim() },
    })
    const row = items.value.find((item) => item.id === edit.id)
    if (row) {
      row.title = updated.title
      row.description = updated.description
    }
    closeEdit()
    flash('作品信息已更新')
  } catch (error) {
    edit.error = errorMessage(error)
  } finally {
    edit.saving = false
  }
}

async function remove(work: Work) {
  if (busy.value[work.id]) return
  busy.value = { ...busy.value, [work.id]: true }
  message.value = ''
  try {
    await api(`/api/v1/contents/${work.id}`, { method: 'DELETE' })
    items.value = items.value.filter((entry) => entry.id !== work.id)
    confirming.value = null
    flash('作品已删除')
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    busy.value = { ...busy.value, [work.id]: false }
  }
}

onMounted(load)
onBeforeUnmount(() => {
  if (savedTimer) clearTimeout(savedTimer)
  clearCoverPick()
})
</script>

<template>
  <section class="works-panel">
    <header class="head">
      <h2>作品管理</h2>
      <span v-if="saved" class="saved" role="status">{{ saved }}</span>
      <span v-else-if="items.length" class="meta">共 {{ items.length }} 件</span>
    </header>

    <p v-if="message" class="notice" role="alert">{{ message }}</p>
    <VpAsyncState
      :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="3"
      empty-title="还没有作品"
      empty-hint="发布第一个作品后，可以在这里改标题、简介与可见性。"
      @retry="load()"
    >
      <template #empty><NuxtLink to="/publish">去发布</NuxtLink></template>
      <ul class="list">
      <li v-for="work in items" :key="work.id">
        <div class="main">
          <NuxtLink class="title" :to="`/${work.type}/${work.slug}`">
            {{ work.title || '（无标题）' }}
          </NuxtLink>
          <span class="badge" :class="statusOf(work).tone">{{ statusOf(work).text }}</span>
          <span class="kind">{{ work.type === 'image' ? '图集' : '视频' }}</span>
        </div>

        <div class="actions">
          <button type="button" class="btn" @click="openEdit(work)">编辑</button>
          <VpSelect
            :model-value="work.visibility" :options="visibilityOptions"
            :disabled="busy[work.id]" :aria-label="`${work.title || '作品'}的可见性`"
            @change="changeVisibility(work, $event)"
          />
          <template v-if="confirming === work.id">
            <button type="button" class="btn danger" :disabled="busy[work.id]" @click="remove(work)">
              确认删除
            </button>
            <button type="button" class="btn" @click="confirming = null">取消</button>
          </template>
          <button v-else type="button" class="btn" @click="confirming = work.id">删除</button>
        </div>
      </li>
    </ul>

    </VpAsyncState>

    <VpDialog
      :open="edit.open" title="编辑作品" :busy="edit.saving || edit.coverBusy"
      @close="closeEdit"
    >
        <div v-if="edit.type === 'video'" class="cover-field">
          <span class="cover-label">封面</span>
          <div class="cover-body">
            <div class="cover-thumb">
              <img
                v-if="edit.coverPreview || edit.coverUrl"
                :src="edit.coverPreview || edit.coverUrl" alt="作品封面"
              />
              <span v-else class="cover-placeholder">暂无封面</span>
            </div>
            <div class="cover-copy">
              <div class="cover-actions">
                <label class="btn cover-pick">
                  {{ edit.coverPreview ? '重新选择' : '更换封面' }}
                  <input
                    type="file" accept="image/png,image/jpeg,image/webp" hidden
                    :disabled="edit.saving || edit.coverBusy" @change="onCoverPick"
                  />
                </label>
                <button
                  v-if="edit.coverPreview" type="button" class="btn"
                  :disabled="edit.saving || edit.coverBusy" @click="clearCoverPick"
                >撤销</button>
              </div>
              <small>
                {{ edit.coverPreview ? '保存后生效，首页与详情页封面会一起更新' : 'PNG / JPEG / WebP，≤8 MiB' }}
              </small>
            </div>
          </div>
        </div>
        <template v-else>
          <div class="cover-field">
            <span class="cover-label">封面（图集首图）</span>
            <div class="cover-body">
              <div class="cover-thumb">
                <img v-if="galleryImages[0]" :src="galleryImages[0].url" alt="图集封面" />
                <span v-else class="cover-placeholder">暂无封面</span>
              </div>
              <div class="cover-copy">
                <small>拖拽调整顺序，首图即封面。</small>
                <label class="btn gallery-add" :class="{ busy: galleryBusy }">
                  {{ galleryBusy ? '处理中…' : '添加图片' }}
                  <input
                    type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden
                    :disabled="galleryBusy || galleryImages.length >= 18" @change="addGalleryImage"
                  />
                </label>
              </div>
            </div>
          </div>
          <ul v-if="galleryImages.length" class="gallery-picks" aria-label="图集图片（可拖拽排序）">
            <li
              v-for="(image, index) in galleryImages" :key="image.id"
              :draggable="!galleryBusy"
              @dragstart="galleryDragStart(index, $event)"
              @dragover.prevent
              @drop="galleryDrop(index, $event)"
            >
              <img :src="image.url" :alt="`第 ${index + 1} 张`" />
              <span v-if="index === 0" class="pick-badge">封面</span>
              <span v-else class="pick-badge ghost" aria-hidden="true">⠿</span>
              <div class="pick-ops">
                <button
                  v-if="index !== 0" type="button" class="btn pick" :disabled="galleryBusy"
                  @click="setGalleryCover(image.id)"
                >设为封面</button>
                <button
                  type="button" class="btn pick danger" :disabled="galleryBusy || galleryImages.length <= 2"
                  :aria-label="`删除第 ${index + 1} 张图片`"
                  @click="removeGalleryImage(image.id)"
                >删除</button>
              </div>
            </li>
          </ul>
          <p v-if="galleryImages.length && galleryImages.length <= 2" class="gallery-min-hint">
            图集至少保留 2 张图片，删到下限时删除按钮会停用。
          </p>
        </template>
        <label class="field">
          <span>标题（≤30 字）</span>
          <input v-model="edit.title" class="text" maxlength="30" aria-label="作品标题" />
        </label>
        <label class="field">
          <span>简介（≤200 字）</span>
          <textarea v-model="edit.description" class="text area" rows="3" maxlength="200" aria-label="作品简介" />
        </label>
        <p v-if="edit.error" class="err" role="alert">{{ edit.error }}</p>
        <div class="dialog-actions">
          <button type="button" class="btn" @click="closeEdit">取消</button>
          <button type="button" class="btn primary" :disabled="edit.saving" @click="saveEdit">
            {{ edit.saving ? '保存中…' : '保存' }}
          </button>
        </div>
    </VpDialog>
  </section>
</template>

<style scoped>
.works-panel { max-width: var(--vp-layout-content-max); margin: 0 auto; }
.head { display: flex; align-items: baseline; gap: 10px; margin-bottom: 10px; }
.head h2 { margin: 0; font-size: 13px; font-weight: 600; }
.meta, .saved { margin-left: auto; font-size: 11px; color: var(--vp-tool-text-2); }
.saved { color: var(--vp-tool-accent-ink); }

.list { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
.list li {
  display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
  padding: 10px 14px; border-radius: 14px;
  background: var(--vp-tool-surface);
  border: 1px solid var(--vp-tool-border);
}
.main { flex: 1 1 240px; min-width: 0; display: flex; align-items: center; gap: 8px; }
.title {
  min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-size: 13px; color: inherit; text-decoration: none;
}
.title:hover { color: var(--vp-tool-accent-ink); }
.badge {
  flex: none; padding: 1px 7px; border-radius: 4px; font-size: 10px;
  border: 1px solid currentColor;
}
.badge.positive { color: var(--vp-tool-positive); }
.badge.attention { color: var(--vp-tool-accent-ink); }
.badge.danger { color: var(--vp-tool-danger); }
.badge.neutral { color: var(--vp-tool-neutral); }
.kind { flex: none; font-size: 10px; color: var(--vp-tool-text-2); }

.actions { display: flex; align-items: center; gap: 6px; margin-left: auto; }
.actions :deep(.vp-select) { width: 96px; }
.btn {
  padding: 6px 12px; border-radius: 99px; cursor: pointer; font-size: 11px;
  border: 1px solid var(--vp-tool-border-strong); background: transparent;
  color: var(--vp-tool-text);
}
.btn.danger { border-color: var(--vp-tool-danger-border); color: var(--vp-tool-danger-ink); }
.btn.primary { border-color: transparent; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); font-weight: 600; }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }

.field { display: grid; gap: 6px; margin-bottom: 10px; }

/* 封面行：左侧 16:9 缩略图，右侧操作与说明（§8.1）。 */
.cover-field { display: grid; gap: 6px; margin-bottom: 12px; }
.cover-label { font-size: 12px; color: var(--vp-tool-text-2); }
.cover-body { display: flex; gap: 12px; align-items: flex-start; }
.cover-thumb {
  flex: none; width: 128px; aspect-ratio: 16 / 9; overflow: hidden;
  display: grid; place-items: center; border-radius: 8px;
  background: var(--vp-tool-surface-2); border: 1px solid var(--vp-tool-border);
  font-size: 11px; color: var(--vp-tool-text-2);
}
.cover-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
.cover-copy { display: grid; gap: 6px; min-width: 0; }
.cover-actions { display: flex; align-items: center; gap: 8px; }
.cover-pick { display: inline-flex; align-items: center; }
.cover-copy small { font-size: 11px; color: var(--vp-tool-text-2); line-height: 1.5; }
.cover-note { margin: 0 0 12px; font-size: 11px; color: var(--vp-tool-text-2); }

/* 图集缩略图条：首图带「封面」角标；全部可拖拽排序，支持增删（已发布也可用）。 */
.gallery-picks {
  list-style: none; margin: 0 0 12px; padding: 0;
  display: flex; gap: 8px; overflow-x: auto; padding-bottom: 2px;
}
.gallery-picks li { position: relative; flex: none; width: 96px; display: grid; gap: 6px; }
.gallery-picks li[draggable='true'] { cursor: grab; }
.gallery-picks li[draggable='true']:active { cursor: grabbing; }
.gallery-add { display: inline-block; margin-top: 8px; cursor: pointer; }
.gallery-add.busy { opacity: 0.5; cursor: wait; }
.gallery-add input { display: none; }
.pick-badge.ghost { opacity: 0.55; }
.pick-ops { display: grid; gap: 4px; margin-top: 6px; }
.pick-ops .btn { padding: 4px 0; font-size: 10px; }
.pick-ops .btn.danger { border-color: rgba(255, 99, 88, 0.55); color: var(--vp-tool-danger, #ff6b5e); }
.pick-ops .btn:disabled { opacity: 0.45; cursor: not-allowed; }
.gallery-min-hint { margin: -4px 0 12px; font-size: 11px; color: var(--vp-tool-text-2); }
.gallery-picks img {
  width: 96px; height: 54px; object-fit: cover; display: block;
  border-radius: 8px; border: 1px solid var(--vp-tool-border);
}
.pick-badge {
  position: absolute; top: 4px; left: 4px; padding: 1px 6px; border-radius: 4px;
  font-size: 10px; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent);
}
.gallery-picks .pick { width: 100%; padding: 4px 0; font-size: 10px; }
.field > span { font-size: 12px; color: var(--vp-tool-text-2); }
.text {
  width: 100%; box-sizing: border-box; padding: 9px 11px; border-radius: 9px;
  border: 1px solid var(--vp-tool-input-border); background: var(--vp-tool-input);
  color: var(--vp-tool-text); font-size: 13px; font-family: inherit;
}
.area { resize: vertical; }
.text:focus-visible { outline: none; border-color: var(--vp-tool-accent-ink); box-shadow: 0 0 0 3px var(--vp-tool-focus); }
.dialog-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 14px; }
.err { margin: 4px 0 0; font-size: 12px; color: var(--vp-tool-danger-ink); }

.notice {
  margin: 0 0 10px; padding: 9px 12px; border-radius: 10px;
  background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink); font-size: 12px;
}
.hint { margin: 30px 0; text-align: center; font-size: 12px; color: var(--vp-tool-text-2); }
.hint a { color: var(--vp-tool-accent-ink); }

@media (max-width: 560px) {
  .actions { width: 100%; margin-left: 0; }
  .actions :deep(.vp-select) { flex: 1; width: auto; }
}
</style>
