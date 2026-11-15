<script setup lang="ts">
useSeoMeta({ robots: 'noindex,nofollow' })
// M1 收尾 · 桌面发布页（Page 3 深色后台基线）
// 视频字节由浏览器经预签名 URL 直传对象存储，不经过 Go API 或 Nuxt。
// 链路：票据 → 分片 PUT(ETag) → 幂等完成 → Worker 转码 → 填写元数据 → 发布 → 播放签名。
interface Me { user_id: number; handle: string; roles: string[] }
interface PresignedPart { part_number: number; url: string }
interface Ticket {
  upload_job_id: number; content_id: number; object_key: string; upload_id: string
  part_size: number; parts: PresignedPart[]; expires_at: string
}
interface UploadStatus {
  upload_job_id: number; content_id: number; upload_status: string
  transcode_job_id?: number; transcode_status?: string; last_error?: string
}
interface PlaybackFile { key: string; kind: string; content_type: string; byte_size: number; url: string }
interface Playback {
  status: string; mode: string; signed: boolean; duration_ms: number
  renditions: string[]; files: PlaybackFile[]; media_url?: string; poster_url?: string
  playlists?: Record<string, string>; expires_at?: string
}

const { api, upload, getMe, logout, clearCsrf } = useApi()

const { open: openAuthDialog } = useAuthDialog()
const { preferences, load: loadPreferences } = usePreferences()
const route = useRoute()

const ALLOWED_MIME = new Set([
  'video/mp4', 'video/quicktime', 'video/webm',
  'video/x-matroska', 'video/x-msvideo', 'video/mpeg',
])
const MAX_BYTES = 2 * 1024 * 1024 * 1024

const me = ref<Me | null>(null)
const checking = ref(true)
const file = ref<File | null>(null)
const title = ref('')
const description = ref('')
const visibility = ref('public')
const scheduledAt = ref('')
// §8.1 字段规格：话题标签、POI；§6.1：草稿自动保存（防抖）与复用。
interface POIValue { name: string; address: string }
const topicIds = ref<number[]>([])
const poi = ref<POIValue | null>(null)
const draftId = ref<number | null>(null)
const draftStatus = ref('')
const savingDraft = ref(false)
// §8.1 图集发布：与视频模式共用字段与设置，切换的是媒体输入与提交链路。
type PublishMode = 'video' | 'image'
interface GalleryItem {
  id: number; position: number; url: string
  mime_type: string; byte_size: number
}
const mode = ref<PublishMode>('video')
const galleryState = ref({ count: 0, ready: 0, busy: false, valid: false, coverUrl: '' })
const galleryRef = ref<{
  hydrate: (items: GalleryItem[]) => void
  reset: () => void
  orderedIds: () => number[]
} | null>(null)
const publishedSlug = ref('')
// 发布响应里的审核状态：pending = 待审核（暂不出现在公开页），approved = 已公开。
const publishedReview = ref('')
const settingsRef = ref<{ scheduleError: string } | null>(null)
const topicPickerRef = ref<{ hydrate: (items: { id: number; slug: string; name: string; status: string }[]) => void } | null>(null)
const busy = ref(false)
const stage = ref('待上传')
// 按钮的阶段感知文案（§8.1 上传进度可见）：用户视线在按钮上，进度不在下方进度区时也能看到进展。
const busyLabel = computed(() => {
  if (!busy.value) return mode.value === 'image' ? '发布图集' : '上传并发布'
  const part = stage.value.match(/^上传分片 (\d+)\/(\d+)$/)
  if (part) return `上传中 ${Math.round(((Number(part[1]) - 1) / Number(part[2])) * 100)}%`
  if (stage.value.startsWith('转码状态')) return '转码中…'
  if (stage.value === '提交转码任务') return '提交转码任务…'
  if (stage.value === '保存作品信息' || stage.value === '创建上传票据') return '准备上传…'
  if (stage.value === '上传图集' || stage.value.startsWith('图集图片')) return '上传图片中…'
  return '处理中…'
})
const progress = ref(0)
const uploadedBytes = ref(0)
const logs = ref<string[]>([])
const errorText = ref('')
const published = ref(false)
const playback = ref<Playback | null>(null)

onMounted(async () => {
  me.value = await getMe()
  checking.value = false
  if (!me.value) return
  // 隐私设置里的「新作品默认可见性」在这里生效；单条作品仍可改。
  await loadPreferences()
  visibility.value = preferences.value.defaultVisibility
  await restoreDraft()
})

// 编辑过程自动保存草稿（§6.1）：字段变化防抖 900ms 落库，
// 发布成功后冻结，避免把已发布内容又改回草稿字段。
let draftTimer: ReturnType<typeof setTimeout> | null = null
let restoringDraft = false
watch([title, description, visibility, poi, topicIds, scheduledAt], () => {
  if (restoringDraft || published.value || busy.value) return
  if (draftTimer) clearTimeout(draftTimer)
  draftTimer = setTimeout(() => { void saveDraft() }, 900)
})
onBeforeUnmount(() => {
  if (draftTimer) clearTimeout(draftTimer)
  if (localPreviewUrl.value) URL.revokeObjectURL(localPreviewUrl.value)
})

function draftPayload() {
  return {
    title: title.value,
    description: description.value,
    visibility: visibility.value,
    poi: poi.value ? { name: poi.value.name, address: poi.value.address } : {},
    scheduled_at: scheduledAt.value || null,
  }
}

async function saveDraft(force = false): Promise<boolean> {
  if (!me.value || busy.value || savingDraft.value) return false
  const hasContent = title.value.trim() || description.value.trim()
    || topicIds.value.length || (mode.value === 'image' && galleryState.value.count > 0)
  if (!draftId.value && !hasContent && !force) return false
  savingDraft.value = true
  draftStatus.value = '正在自动保存草稿…'
  try {
    if (!draftId.value) {
      const created = await api<{ id: number }>('/api/v1/contents', {
        method: 'POST',
        body: { type: mode.value, title: title.value, description: description.value },
      })
      draftId.value = created.id
    }
    await api(`/api/v1/contents/${draftId.value}`, { method: 'PATCH', body: draftPayload() })
    await api(`/api/v1/contents/${draftId.value}/topics`, {
      method: 'PUT', body: { topic_ids: topicIds.value },
    })
    draftStatus.value = '草稿已保存'
    return true
  } catch (error) {
    // 内容被拒时直接展示服务端理由，别套「保存失败」把真实原因盖住。
    draftStatus.value = errorMessage(error)
    return false
  } finally {
    savingDraft.value = false
  }
}

// 恢复最近一条未发布草稿，避免刷新页面丢内容。
async function restoreDraft() {
  // ?new=1 显式新建（不恢复任何草稿）；?draft=<id> 从草稿箱继续编辑。
  // 两者都没有时，保持「恢复最近一条草稿」的兜底行为，避免刷新丢内容。
  const isNew = route.query.new === '1'
  const requested = Number(route.query.draft || 0)
  if (isNew) return
  restoringDraft = true
  try {
    const data = await api<{ items: Array<{ id: number; type: string; title: string; description: string; visibility: string; poi?: POIValue; publish_state: string; scheduled_at?: string }> }>(
      '/api/v1/contents/mine?limit=100&state=draft',
    )
    const drafts = (data.items || []).filter((item) => item.publish_state === 'draft')
    let draft = drafts[0]
    let statusText = drafts.length ? '已恢复上次的草稿' : ''
    if (requested > 0) {
      draft = drafts.find((item) => item.id === requested)
      if (!draft) {
        // 不存在 / 属于他人 / 已删除 / 已发布：明确提示，不静默打开空白页
        draftStatus.value = '该草稿不存在或已被删除，已为你打开新建页面'
        return
      }
      statusText = '已从草稿箱恢复'
    }
    if (!draft) return
    draftId.value = draft.id
    mode.value = draft.type === 'image' ? 'image' : 'video'
    title.value = draft.title || ''
    description.value = draft.description || ''
    visibility.value = draft.visibility || 'public'
    if (draft.poi?.name) poi.value = { name: draft.poi.name, address: draft.poi.address || '' }
    if (draft.scheduled_at) scheduledAt.value = draft.scheduled_at
    const topics = await api<{ items: Array<{ id: number; slug: string; name: string; status: string }> }>(
      `/api/v1/contents/${draft.id}/topics`,
    )
    topicPickerRef.value?.hydrate(topics.items || [])
    topicIds.value = (topics.items || []).map((item) => item.id)
    if (mode.value === 'image') {
      const images = await api<{ items: GalleryItem[] }>(`/api/v1/contents/${draft.id}/images`)
      await nextTick()
      galleryRef.value?.hydrate(images.items || [])
    }
    draftStatus.value = statusText
  } catch { /* 没有草稿或暂不可用时保持空白编辑态 */ }
  finally {
    await nextTick()
    restoringDraft = false
  }
}

// 图集上传前需要一条 type=image 的草稿；组件在第一张图上传时调用。
async function ensureGalleryDraft(): Promise<number | null> {
  if (draftId.value) return draftId.value
  const ok = await saveDraft(true)
  return ok ? draftId.value : null
}

function onGalleryChange(next: { count: number; ready: number; busy: boolean; valid: boolean; coverUrl: string }) {
  galleryState.value = next
}

async function setMode(next: PublishMode) {
  if (next === mode.value || busy.value) return
  // 已有草稿且类型不同：不再复用，让自动保存 / 上传新建对应类型的草稿。
  // 旧草稿保留在草稿箱，不会丢数据。
  if (draftId.value) {
    draftId.value = null
    draftStatus.value = '已切换发布模式，接下来会保存为一条新草稿'
  }
  galleryRef.value?.reset()
  pendingCover.value = null
  coverError.value = ''
  mode.value = next
  errorText.value = ''
  publishedSlug.value = ''
}

async function submitGallery() {
  if (!galleryState.value.valid) {
    errorText.value = galleryState.value.count < 2
      ? '图集至少需要 2 张图片'
      : '请等待全部图片上传完成'
    return
  }
  if (!draftId.value) {
    const ok = await saveDraft(true)
    if (!ok || !draftId.value) { errorText.value = '草稿保存失败，无法发布'; return }
  }
  busy.value = true
  logs.value = []
  progress.value = 0
  stage.value = '发布图集'
  try {
    log(`提交图集 · ${galleryState.value.count} 张图片…`)
    // 拖拽排序是异步 PUT，发布前再兑一次，保证服务端顺序与编辑器一致。
    const ids = galleryRef.value?.orderedIds() || []
    if (ids.length === galleryState.value.count && ids.length >= 2) {
      await api(`/api/v1/contents/${draftId.value}/images/order`, {
        method: 'PUT', body: { ids },
      })
    }
    const publishBody: Record<string, unknown> = {
      visibility: visibility.value,
      poi: poi.value ? { name: poi.value.name, address: poi.value.address } : {},
    }
    if (scheduledAt.value) publishBody.scheduled_at = new Date(scheduledAt.value).toISOString()
    const result = await api<{ slug: string; review_status: string }>(
      `/api/v1/contents/${draftId.value}/publish`, { method: 'POST', body: publishBody },
    )
    published.value = true
    publishedSlug.value = result.slug
    publishedReview.value = result.review_status || 'pending'
    draftStatus.value = scheduledAt.value ? '已提交定时发布' : '已发布'
    progress.value = 100
    stage.value = '已完成'
    log('图集发布成功，沉浸流内可左右横滑切换图片')
    // 发布成功后重置草稿引用与图集状态（旧 content 已非 draft）。
    draftId.value = 0
    galleryState.value = { count: 0, ready: 0, busy: false, valid: false, coverUrl: '' }
  } catch (error) {
    stage.value = '失败'
    errorText.value = errorMessage(error)
    log(`失败：${errorMessage(error)}`)
  } finally {
    busy.value = false
  }
}

function log(message: string) {
  const time = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  logs.value = [`[${time}] ${message}`, ...logs.value].slice(0, 30)
}

function mimeFor(value: File): string {
  if (value.type) return value.type
  const ext = value.name.split('.').pop()?.toLowerCase() || ''
  const guesses: Record<string, string> = {
    mp4: 'video/mp4', mov: 'video/quicktime', webm: 'video/webm',
    mkv: 'video/x-matroska', avi: 'video/x-msvideo', mpg: 'video/mpeg', mpeg: 'video/mpeg',
  }
  return guesses[ext] || ''
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 ** 2).toFixed(1)} MB`
}

function onFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  file.value = input.files?.[0] || null
  errorText.value = ''
  // 选了新文件 = 全新发布：重置上一单的发布后状态（draftId 已随发布结束，
  // 自动保存跳过、再次上传票据会被拒——见 saveDraft 与 upload 票据校验）。
  published.value = false
  publishedSlug.value = ''
  publishedReview.value = ''
  draftId.value = 0
  stage.value = '待上传'
  progress.value = 0
  logs.value = []
}

// 本地视频预览（§8.1「视频预览可播放校验」）：选完文件即可播放校验画面与声音，
// 并读取时长显示在文件信息里；本地预览不上传任何字节。
const localPreviewUrl = ref('')
const durationText = ref('')
watch(file, (next) => {
  if (localPreviewUrl.value) URL.revokeObjectURL(localPreviewUrl.value)
  localPreviewUrl.value = next && import.meta.client ? URL.createObjectURL(next) : ''
  durationText.value = ''
})
function onPreviewMetadata(event: Event) {
  const video = event.target as HTMLVideoElement
  if (!Number.isFinite(video.duration) || video.duration <= 0) return
  const total = Math.round(video.duration)
  durationText.value = `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

// 视频封面：转码产物 poster 作回退；用户选帧 / 自定义上传先暂存，
// 转码完成后由发布流程写版本化 poster（§8.1），不依赖服务端抽源视频。
const coverUrl = computed(() => playback.value?.poster_url || '')
const pendingCover = ref<{ blob: Blob; previewUrl: string } | null>(null)
const coverError = ref('')
function onCoverChange(payload: { blob: Blob | null; previewUrl: string }) {
  pendingCover.value = payload.blob
    ? { blob: payload.blob, previewUrl: payload.previewUrl }
    : null
  if (payload.blob) coverError.value = ''
}
function onCoverError(message: string) {
  coverError.value = message
}

async function waitForTranscode(uploadJobID: number): Promise<void> {
  for (let i = 0; i < 80; i++) {
    const state = await api<UploadStatus>(`/api/v1/uploads/${uploadJobID}`)
    stage.value = `转码状态：${state.transcode_status || state.upload_status}`
    if (state.transcode_status === 'published') return
    if (state.transcode_status === 'transcode_failed') {
      throw new Error(`转码失败：${state.last_error || '未知原因'}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 1500))
  }
  throw new Error('转码等待超时，请确认 Worker 已启动')
}

async function submit() {
  errorText.value = ''
  playback.value = null
  published.value = false
  publishedSlug.value = ''
  publishedReview.value = ''
  const titleLength = [...title.value].length
  if (titleLength === 0 || titleLength > 30) { errorText.value = '标题需为 1–30 个字符'; return }
  if ([...description.value].length > 200) { errorText.value = '描述不能超过 200 个字符'; return }
  const scheduleError = settingsRef.value?.scheduleError
  if (scheduleError) { errorText.value = scheduleError; return }
  if (mode.value === 'image') { await submitGallery(); return }

  const selected = file.value
  if (!selected) { errorText.value = '请先选择视频文件'; return }
  const mime = mimeFor(selected)
  if (!ALLOWED_MIME.has(mime)) { errorText.value = `不支持的格式：${mime || selected.name}`; return }
  if (selected.size > MAX_BYTES) { errorText.value = '文件超过 2 GiB 上限'; return }

  // 必须在 busy=true 之前保存：saveDraft 在忙状态下会跳过，
  // 那样票据就拿不到可复用的草稿 id，会新建一条空 content。
  stage.value = '保存作品信息'
  log('先确认草稿已落库…')
  await saveDraft(true)

  busy.value = true
  logs.value = []
  progress.value = 0
  uploadedBytes.value = 0
  try {
    const ticketBody: Record<string, unknown> = {
      content_type: mime,
      byte_size: selected.size,
      idempotency_key: crypto.randomUUID(),
    }
    // 复用自动保存的草稿，避免上传票据再建一条空 content（§6.1）。
    if (draftId.value) ticketBody.content_id = draftId.value
    stage.value = '创建上传票据'
    log('创建幂等上传票据…')
    const ticket = await api<Ticket>('/api/v1/uploads', {
      method: 'POST',
      body: ticketBody,
    })
    log(`票据 #${ticket.upload_job_id} · ${ticket.parts.length} 个分片 · 每片 8 MiB`)

    const parts: { part_number: number; etag: string }[] = []
    for (const part of ticket.parts) {
      stage.value = `上传分片 ${part.part_number}/${ticket.parts.length}`
      const start = (part.part_number - 1) * ticket.part_size
      const end = Math.min(start + ticket.part_size, selected.size)
      const chunk = await selected.slice(start, end).arrayBuffer()
      const response = await fetch(part.url, { method: 'PUT', body: chunk })
      if (!response.ok) {
        throw new Error(`分片 ${part.part_number} 上传失败：HTTP ${response.status}`)
      }
      const etag = response.headers.get('ETag')
      if (!etag) {
        throw new Error(`分片 ${part.part_number} 未返回 ETag（检查对象存储 CORS 暴露头）`)
      }
      parts.push({ part_number: part.part_number, etag })
      uploadedBytes.value = end
      progress.value = Math.round((end / selected.size) * 100)
    }

    stage.value = '提交转码任务'
    log(`${parts.length} 个分片直传完成，提交幂等完成请求…`)
    await api<{ transcode_job_id: number }>(`/api/v1/uploads/${ticket.upload_job_id}/complete`, {
      method: 'POST', body: { parts },
    })
    log('任务已入队（PostgreSQL 为事实源，Redis Stream 仅唤醒）')

    await waitForTranscode(ticket.upload_job_id)
    // 封面必须在转码产物就绪后写：此时 artifact_manifest 已存在，
    // 版本化 poster 会替换清单里的旧封面，全站自动跟随。
    // 失败不阻断主流程，但明确提示，不假装封面已生效。
    if (pendingCover.value) {
      stage.value = '提交自定义封面'
      try {
        const form = new FormData()
        form.append('image', pendingCover.value.blob, 'cover.jpg')
        await upload(`/api/v1/contents/${ticket.content_id}/cover`, form)
        log('自定义封面已写入转码产物清单')
        coverError.value = ''
      } catch (error) {
        coverError.value = `封面未能保存：${errorMessage(error)}；作品仍按自动封面发布`
        log(coverError.value)
      }
    }
    log('转码产物已发布，提交发布设置…')
    if (draftId.value !== ticket.content_id) {
      await api(`/api/v1/contents/${ticket.content_id}`, { method: 'PATCH', body: draftPayload() })
      await api(`/api/v1/contents/${ticket.content_id}/topics`, {
        method: 'PUT', body: { topic_ids: topicIds.value },
      })
    }
    const publishBody: Record<string, unknown> = {
      visibility: visibility.value,
      poi: poi.value ? { name: poi.value.name, address: poi.value.address } : {},
    }
    if (scheduledAt.value) publishBody.scheduled_at = new Date(scheduledAt.value).toISOString()
    const result = await api<{ slug: string; review_status: string }>(
      `/api/v1/contents/${ticket.content_id}/publish`, { method: 'POST', body: publishBody },
    )
    published.value = true
    publishedSlug.value = result.slug
    publishedReview.value = result.review_status || 'pending'
    draftStatus.value = scheduledAt.value ? '已提交定时发布' : '已发布'

    stage.value = '已完成'
    log('发布成功，读取短期播放签名…')
    playback.value = await api<Playback>(`/api/v1/contents/${ticket.content_id}/playback`)
    // 发布成功后重置草稿引用与文件（旧 content 已非 draft，自动保存与再次
    // 上传的票据关联都会被拒）；播放面板保留展示发布产物。
    draftId.value = 0
    file.value = null
  } catch (error) {
    stage.value = '失败'
    errorText.value = errorMessage(error)
    log(`失败：${errorMessage(error)}`)
  } finally {
    busy.value = false
  }
}

// 退出登录是降权操作：两步确认（点一次变「确认退出」，3 秒后还原），
// 避免误点或被脚本顺手带走；不要求再输密码，否刚忘密码就退不出去。
const confirmSignOut = ref(false)
let signOutTimer: ReturnType<typeof setTimeout> | null = null

function requestSignOut() {
  if (confirmSignOut.value) {
    confirmSignOut.value = false
    if (signOutTimer) { clearTimeout(signOutTimer); signOutTimer = null }
    void signOut()
    return
  }
  confirmSignOut.value = true
  if (signOutTimer) clearTimeout(signOutTimer)
  signOutTimer = setTimeout(() => { confirmSignOut.value = false }, 3000)
}

async function signOut() {
  try { await logout() } catch { /* session may already be gone */ }
  clearCsrf()
  me.value = null
}
</script>

<template>
  <div class="publish" data-surface="immersive">
    <!-- 左：导航栏（UI 规范 §8.1 三段式） -->
    <aside class="rail" aria-label="主导航">
      <NuxtLink to="/" class="brand">夜街<span class="brand-en">YEJIE</span></NuxtLink>
      <div class="rail-label">创作者工作台</div>
      <nav class="rail-nav" aria-label="站点导航">
        <NuxtLink to="/" class="nav-item"><span class="nav-icon" aria-hidden="true">⌂</span>首页</NuxtLink>
        <NuxtLink to="/?channel=follow" class="nav-item"><span class="nav-icon" aria-hidden="true">♡</span>朋友</NuxtLink>
        <span class="nav-item disabled" aria-disabled="true" title="同城频道尚未开放">
          <span class="nav-icon" aria-hidden="true">◎</span>同城<em>即将开放</em>
        </span>
        <NuxtLink to="/messages" class="nav-item"><span class="nav-icon" aria-hidden="true">✉</span>消息</NuxtLink>
        <NuxtLink to="/me" class="nav-item"><span class="nav-icon" aria-hidden="true">◉</span>我</NuxtLink>
      </nav>
      <NuxtLink to="/publish?new=1" class="rail-cta">＋ 发布作品</NuxtLink>
      <div class="rail-user">
        <template v-if="me">
          <span class="avatar">{{ me.handle.slice(0, 1).toUpperCase() }}</span>
          <div class="rail-user-text">
            <strong>夜街号：{{ me.handle }}</strong>
            <NuxtLink to="/studio">创作中心</NuxtLink>
          </div>
          <button
            type="button" class="sign-out" :class="{ danger: confirmSignOut }"
            @click="requestSignOut"
          >{{ confirmSignOut ? '确认退出' : '退出' }}</button>
        </template>
        <button v-else-if="!checking" type="button" class="rail-login" @click="openAuthDialog()">登录 / 注册</button>
        <span v-else class="rail-user-text">正在检查登录…</span>
      </div>
    </aside>

    <!-- 中：作品区（上传与预览、处理状态、封面状态条） -->
    <main class="stage-col">
      <header class="stage-head">
        <h1>发布作品</h1>
        <p>视频自动转码为 HLS / MP4 并生成封面与预览索引；图集最多 18 张、首图为封面。</p>
      </header>

      <section v-if="checking" class="gate">正在检查登录状态…</section>
      <section v-else-if="!me" class="gate">
        <h2>请先登录</h2>
        <p>登录后即可上传、转码并发布作品。</p>
        <button type="button" class="primary-link" @click="openAuthDialog()">前往登录</button>
      </section>

      <template v-else>
        <div class="mode-switch" role="tablist" aria-label="发布形态">
          <button
            type="button" role="tab" :aria-selected="mode === 'video'"
            :class="{ on: mode === 'video' }" :disabled="busy"
            @click="setMode('video')"
          >视频</button>
          <button
            type="button" role="tab" :aria-selected="mode === 'image'"
            :class="{ on: mode === 'image' }" :disabled="busy"
            @click="setMode('image')"
          >图集</button>
          <span class="mode-hint">{{ mode === 'image' ? '2–18 张图片，首图为封面' : '单条视频，自动转码' }}</span>
        </div>

        <section class="work-area" aria-label="作品文件与预览">
          <h2>1 · {{ mode === 'image' ? '选择图片' : '选择视频' }}</h2>
          <template v-if="mode === 'video'">
            <label class="drop" :class="{ filled: !!file }">
              <input type="file" accept="video/*" :disabled="busy" @change="onFileChange" />
              <span class="drop-icon" aria-hidden="true">＋</span>
              <strong>{{ file ? file.name : '点击选择或拖入视频文件' }}</strong>
              <small>{{ file ? `${formatBytes(file.size)} · ${mimeFor(file)}` : '支持 MP4 / MOV / WebM / MKV，最大 2 GiB' }}</small>
            </label>
            <div v-if="file" class="file-meta">
              <span>大小 {{ file ? formatBytes(file.size) : '' }}</span>
              <span v-if="durationText">时长 {{ durationText }}</span>
              <span>类型 {{ file ? mimeFor(file) : '' }}</span>
            </div>
            <video
              v-if="localPreviewUrl" class="local-preview" controls playsinline
              :src="localPreviewUrl" @loadedmetadata="onPreviewMetadata"
            />
            <p class="note">媒体字节由浏览器直传对象存储；应用进程只签发 URL，不转发文件。选择后可在上方预览校验画面与声音。</p>
          </template>
          <template v-else>
            <ImageGalleryEditor
              ref="galleryRef"
              :content-id="draftId"
              :ensure-draft="ensureGalleryDraft"
              :disabled="busy"
              @change="onGalleryChange"
            />
          </template>
        </section>

        <CoverPicker
          v-if="mode === 'video'"
          :video-file="file"
          :current-url="coverUrl"
          :disabled="busy"
          @change="onCoverChange"
          @error="onCoverError"
        />
        <section v-else class="cover-strip" aria-label="封面状态">
          <div class="cover-thumb">
            <img v-if="galleryState.coverUrl" :src="galleryState.coverUrl" alt="图集首图封面" />
            <span v-else class="cover-placeholder">封面</span>
          </div>
          <div class="cover-info">
            <strong>封面</strong>
            <p>图集以首图为封面，可在上方拖拽或使用「设为封面」按钮调整。</p>
          </div>
        </section>
        <p v-if="coverError" class="cover-error" role="alert">{{ coverError }}</p>

        <section class="progress-area" aria-label="上传与处理状态">
          <h2>2 · 处理进度</h2>
          <div class="stage"><span class="pulse" :class="{ done: published }" />{{ stage }}</div>
          <div class="bar"><div :style="{ width: `${progress}%` }" /></div>
          <p class="counter">{{ mode === 'image' ? `${galleryState.ready}/${galleryState.count} 张图片已就绪` : `${formatBytes(uploadedBytes)} / ${file ? formatBytes(file.size) : '0 B'} · ${progress}%` }}</p>

          <div v-if="published && mode === 'image'" class="result ok">
            <h3>图集发布完成</h3>
            <p>共 {{ galleryState.count }} 张图片，首图为封面；沉浸流内可左右横滑切换。</p>
            <p v-if="publishedReview === 'pending'" class="review-hint">
              作品已提交审核，通过后会出现在公开页面；审核状态可在「我」页查看。
            </p>
            <NuxtLink v-if="publishedSlug" class="primary-link" :to="`/image/${publishedSlug}`">查看图集</NuxtLink>
          </div>

          <div v-if="playback" class="result" :class="{ ok: published }">
            <h3>{{ published ? '发布完成' : '产物预览' }}</h3>
            <p v-if="published && publishedReview === 'pending'" class="review-hint">
              作品已提交审核，通过后会出现在公开页面；审核状态可在「我」页查看。
            </p>
            <p>模式 {{ playback.mode }} · 签名 {{ playback.signed ? '30 分钟' : '公共 CDN' }} · {{ playback.files.length }} 个产物</p>
            <video
              v-if="playback.mode === 'mp4' && playback.media_url"
              class="preview-video" controls playsinline
              :poster="playback.poster_url" :src="playback.media_url"
            />
            <img v-else-if="playback.poster_url" class="preview-poster" :src="playback.poster_url" alt="封面预览" />
            <p v-if="playback.mode === 'hls'" class="note">HLS 播放列表已生成并重写为绝对地址。</p>
            <details>
              <summary>产物清单</summary>
              <ul>
                <li v-for="item in playback.files" :key="item.key">
                  <span class="kind">{{ item.kind }}</span>
                  <span class="key">{{ item.key.split('/').slice(-2).join('/') }}</span>
                  <span class="size">{{ formatBytes(item.byte_size) }}</span>
                </li>
              </ul>
            </details>
          </div>

          <h3 class="log-title">事件日志</h3>
          <ul class="logs">
            <li v-for="(line, index) in logs" :key="index">{{ line }}</li>
            <li v-if="!logs.length" class="muted">尚无事件</li>
          </ul>
        </section>
      </template>
    </main>

    <!-- 右：作品信息表单（实色表面，§4.2 表单不用玻璃） -->
    <aside class="form-col" aria-label="作品信息表单">
      <div class="form-head">
        <h2>作品信息</h2>
        <span>字段与设置同移动端</span>
      </div>
      <div v-if="checking || !me" class="form-gate">登录后即可填写作品信息与发布设置。</div>
      <div v-else class="form-body">
        <label class="field">
          <span>标题 <em>{{ [...title].length }}/30</em></span>
          <input v-model="title" :disabled="busy" maxlength="30" placeholder="给这段夜色起个名字" />
        </label>
        <label class="field">
          <span>描述 <em>{{ [...description].length }}/200</em></span>
          <textarea v-model="description" :disabled="busy" maxlength="200" rows="3" placeholder="地点、故事、标签…" />
        </label>
        <div class="field">
          <span>话题标签 <em>{{ topicIds.length }}/8</em></span>
          <TopicPicker ref="topicPickerRef" v-model="topicIds" :disabled="busy" />
        </div>

        <h2 class="form-section">发布设置</h2>
        <PublishSettings
          ref="settingsRef"
          v-model:visibility="visibility"
          v-model:poi="poi"
          v-model:scheduled-at="scheduledAt"
          :disabled="busy"
        />

        <p v-if="draftStatus" class="draft-status" role="status">{{ draftStatus }}</p>
        <div class="publish-actions">
          <button
            class="secondary" type="button" :disabled="busy || savingDraft"
            @click="saveDraft(true)"
          >{{ savingDraft ? '保存中…' : '存为草稿' }}</button>
          <button
            class="primary" type="button"
            :disabled="busy || (mode === 'video' ? !file : !galleryState.valid)"
            @click="submit"
          >
            {{ busy ? busyLabel : (mode === 'image' ? '发布图集' : '上传并发布') }}
          </button>
        </div>
        <p class="note">编辑内容会自动保存为草稿；发布前不会进入公开页面或统计。</p>
        <p v-if="errorText" class="error" role="alert">{{ errorText }}</p>
      </div>
    </aside>
  </div>
</template>
<style scoped>
/* ── 三段式布局（UI 规范 §8.1）─────────────────────────────────────
   左导航 / 中作品区 / 右信息表单。发布页属沉浸内页面，恒深色基线；
   颜色全部消费 :root 上的工具语义令牌，表单为实色表面、不使用玻璃。 */
.publish {
  min-height: 100dvh; display: grid;
  grid-template-columns: 216px minmax(0, 1.25fr) minmax(320px, .9fr);
  background: var(--vp-tool-bg); color: var(--vp-tool-text);
}

/* 左栏 */
.rail {
  display: flex; flex-direction: column; padding: 26px 18px 20px;
  border-right: 1px solid var(--vp-tool-border); background: var(--vp-tool-bg);
}
.brand { display: grid; gap: 2px; color: var(--vp-color-brand); font-size: 22px; font-weight: 700; letter-spacing: .1em; text-decoration: none; }
.brand-en { font-size: 9px; letter-spacing: .32em; font-weight: 500; color: var(--vp-tool-text-2); }
.rail-label { margin-top: 10px; font-size: 11px; color: var(--vp-tool-text-2); }
.rail-nav { margin-top: 28px; display: grid; gap: 3px; }
.nav-item {
  display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-radius: 9px;
  font-size: 13px; color: var(--vp-tool-text-2); text-decoration: none;
  border: 1px solid transparent;
}
.nav-item:hover:not(.disabled) { background: var(--vp-tool-surface-2); color: var(--vp-tool-text); }
.nav-item.router-link-active { background: var(--vp-tool-accent-soft); color: var(--vp-tool-text); }
.nav-icon { width: 16px; flex: none; text-align: center; font-size: 14px; }
.nav-item.disabled { opacity: .65; cursor: not-allowed; }
.nav-item em {
  margin-left: auto; font-style: normal; font-size: 9px; padding: 2px 5px;
  border-radius: 4px; background: var(--vp-tool-surface-2);
}
.rail-cta {
  margin-top: 22px; padding: 11px 14px; border-radius: 10px; text-align: center;
  background: var(--vp-tool-accent); color: var(--vp-tool-on-accent);
  font-size: 13px; font-weight: 600; text-decoration: none;
}
.rail-user {
  margin-top: auto; padding-top: 16px; border-top: 1px solid var(--vp-tool-border);
  display: flex; align-items: center; gap: 9px; font-size: 12px;
}
.avatar { display: grid; place-items: center; width: 30px; height: 30px; flex: none; border-radius: 50%; background: var(--vp-color-brand); color: #fff; font-weight: 700; }
.rail-user-text { display: grid; gap: 2px; min-width: 0; }
.rail-user-text strong { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rail-user-text a { font-size: 11px; color: var(--vp-tool-text-2); text-decoration: none; }
.sign-out { flex: none; margin-left: auto; padding: 4px 7px; font-size: 10px; white-space: nowrap; border-radius: 6px; cursor: pointer; border: 1px solid var(--vp-tool-border-strong); background: transparent; color: var(--vp-tool-text-2); }
.sign-out.danger { color: #ff6b5e; border-color: rgba(255, 107, 94, 0.5); }
.rail-login { border: 0; background: none; padding: 0; cursor: pointer; font-family: inherit; color: var(--vp-tool-accent-ink); text-decoration: none; font-size: 12px; }

/* 中栏 */
.stage-col { min-width: 0; padding: 26px 24px 40px; }
.stage-head { border-bottom: 1px solid var(--vp-tool-border); padding-bottom: 18px; }
.stage-head h1 { margin: 0; font-size: 24px; }
.stage-head p { margin: 6px 0 0; font-size: 12px; line-height: 1.6; color: var(--vp-tool-text-2); }
.gate { padding: 60px 0; text-align: center; }
.gate h2 { margin: 0 0 8px; }
.gate p { color: var(--vp-tool-text-2); font-size: 13px; }

.mode-switch { display: flex; align-items: center; gap: 6px; margin-top: 20px; padding: 4px; border-radius: 10px; background: var(--vp-tool-input); border: 1px solid var(--vp-tool-border); }
.mode-switch button { padding: 7px 16px; border: 0; border-radius: 7px; cursor: pointer; font-size: 12px; font-family: inherit; background: transparent; color: var(--vp-tool-text-2); }
.mode-switch button.on { background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); font-weight: 600; }
.mode-switch button:disabled { opacity: .55; cursor: not-allowed; }
.mode-hint { margin-left: auto; padding-right: 8px; font-size: 11px; color: var(--vp-tool-text-2); }

.work-area, .progress-area {
  margin-top: 16px; padding: 18px; border-radius: 14px;
  border: 1px solid var(--vp-tool-border); background: var(--vp-tool-surface);
}
.work-area h2, .progress-area h2 { margin: 0 0 14px; font-size: 14px; }
.drop {
  position: relative; display: grid; place-items: center; gap: 7px; padding: 30px 16px; text-align: center;
  border: 1px dashed var(--vp-tool-border-strong); border-radius: 12px; cursor: pointer; background: var(--vp-tool-input);
}
.drop.filled { border-color: var(--vp-tool-accent-ink); background: var(--vp-tool-accent-soft); }
.drop input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
.drop-icon { font-size: 26px; color: var(--vp-tool-accent-ink); }
.drop strong { font-size: 13px; font-weight: 500; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.drop small { font-size: 11px; color: var(--vp-tool-text-2); }
.file-meta { display: flex; flex-wrap: wrap; gap: 14px; margin-top: 10px; font-size: 11px; color: var(--vp-tool-text-2); }
.local-preview { display: block; width: 100%; max-height: 260px; margin-top: 12px; border-radius: 9px; object-fit: contain; background: #000; }
.note { margin: 10px 0 0; font-size: 11px; line-height: 1.6; color: var(--vp-tool-text-2); }

.cover-strip { display: flex; gap: 12px; align-items: center; margin-top: 14px; padding: 12px; border: 1px solid var(--vp-tool-border); border-radius: 12px; background: var(--vp-tool-surface); }
.cover-thumb { width: 54px; height: 72px; flex: none; border-radius: 7px; overflow: hidden; background: var(--vp-tool-input); display: grid; place-items: center; }
.cover-thumb img { width: 100%; height: 100%; object-fit: cover; }
.cover-placeholder { font-size: 10px; color: var(--vp-tool-text-2); }
.cover-info { display: grid; gap: 3px; min-width: 0; }
.cover-info strong { font-size: 12px; }
.cover-info p { margin: 0; font-size: 11px; color: var(--vp-tool-text-2); }
.cover-info small { font-size: 10px; color: var(--vp-tool-text-2); opacity: .85; }
.cover-error { margin: 8px 0 0; padding: 8px 10px; border-radius: 8px; font-size: 11px; background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink); }

.stage { display: flex; align-items: center; gap: 9px; font-size: 13px; }
.pulse { width: 8px; height: 8px; border-radius: 50%; background: var(--vp-tool-accent); box-shadow: 0 0 0 4px var(--vp-tool-focus); }
.pulse.done { background: var(--vp-tool-positive); box-shadow: 0 0 0 4px var(--vp-tool-accent-soft); }
.bar { height: 6px; margin-top: 14px; border-radius: 99px; background: var(--vp-tool-surface-2); overflow: hidden; }
.bar div { height: 100%; background: var(--vp-tool-accent); transition: width .2s ease; }
.counter { font-size: 11px; color: var(--vp-tool-text-2); }
.review-hint {
  margin: 6px 0 0; padding: 8px 12px; border-radius: 8px; font-size: 12px;
  background: rgba(255, 176, 32, 0.12); color: var(--vp-tool-text-1, inherit);
  text-align: left;
}
.result { margin-top: 20px; padding: 16px; border: 1px solid var(--vp-tool-border); border-radius: 11px; background: var(--vp-tool-input); }
.result.ok { border-color: var(--vp-tool-positive); }
.result h3 { margin: 0 0 6px; font-size: 14px; }
.result p { margin: 4px 0; font-size: 11px; color: var(--vp-tool-text-2); }
.preview-video, .preview-poster { display: block; width: 100%; max-height: 280px; margin-top: 12px; border-radius: 9px; object-fit: contain; background: #000; }
details { margin-top: 12px; font-size: 11px; }
summary { cursor: pointer; color: var(--vp-tool-text-2); }
.result ul { list-style: none; margin: 10px 0 0; padding: 0; max-height: 190px; overflow: auto; }
.result li { display: flex; gap: 8px; padding: 5px 0; border-bottom: 1px solid var(--vp-tool-divider); }
.result .kind { flex: none; width: 62px; color: var(--vp-tool-positive); }
.result .key { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.result .size { flex: none; color: var(--vp-tool-text-2); }
.log-title { margin: 22px 0 10px; font-size: 12px; color: var(--vp-tool-text-2); }
.logs { list-style: none; margin: 0; padding: 0; max-height: 210px; overflow: auto; font-size: 11px; line-height: 1.9; color: var(--vp-tool-text-2); }
.logs .muted { opacity: .6; }

/* 右栏：实色表单表面 */
.form-col { display: flex; flex-direction: column; min-width: 0; border-left: 1px solid var(--vp-tool-border); background: var(--vp-tool-surface); }
.form-head { padding: 22px 20px 16px; border-bottom: 1px solid var(--vp-tool-border); display: grid; gap: 4px; }
.form-head h2 { margin: 0; font-size: 15px; }
.form-head span { font-size: 11px; color: var(--vp-tool-text-2); }
.form-body { padding: 20px; }
.form-gate { padding: 40px 20px; font-size: 12px; line-height: 1.7; color: var(--vp-tool-text-2); }
.form-section { margin: 22px 0 14px; font-size: 14px; }
.field { display: grid; gap: 7px; margin-bottom: 14px; font-size: 12px; color: var(--vp-tool-text-2); }
.field span { display: flex; justify-content: space-between; }
.field em { font-style: normal; opacity: .8; }
.field input, .field textarea, .field select {
  width: 100%; box-sizing: border-box; padding: 10px 11px; border-radius: 9px; font-size: 13px;
  border: 1px solid var(--vp-tool-input-border); background: var(--vp-tool-input);
  color: var(--vp-tool-text); outline: none; resize: vertical; font-family: inherit;
}
.field input:focus, .field textarea:focus, .field select:focus { border-color: var(--vp-tool-accent-ink); }
/* 主按钮用 M16 校验过的强调色对：白字在亮粉底上只有 3.53:1，改用近黑字。 */
.primary { width: 100%; margin-top: 6px; padding: 12px; border: 0; border-radius: 10px; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); font-weight: 600; cursor: pointer; font-family: inherit; }
.primary:disabled { opacity: .55; cursor: not-allowed; }
.publish-actions { display: flex; gap: 10px; margin-top: 14px; }
.publish-actions .primary { flex: 1.4; margin-top: 0; }
.secondary { flex: 1; padding: 12px; border-radius: 10px; cursor: pointer; font-size: 13px; font-family: inherit; border: 1px solid var(--vp-tool-border-strong); background: transparent; color: var(--vp-tool-text); }
.secondary:disabled { opacity: .55; cursor: not-allowed; }
.draft-status { margin: 8px 0 0; font-size: 11px; color: var(--vp-tool-text-2); }
.error { margin: 12px 0 0; padding: 9px 11px; border-radius: 8px; background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink); font-size: 12px; }
.login-link, .primary-link { display: inline-block; padding: 9px 16px; border-radius: 9px; border: 0; cursor: pointer; font-family: inherit; text-decoration: none; font-size: 13px; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); }

/* 窄屏 < 1024px：回落移动端沉浸内发布形态，不做三段式压缩硬撑（§8.1） */
@media (max-width: 1023px) {
  /* 单栏时禁止 grid 把 min-height 的剩余空间拉伸成行间空白。 */
  .publish { grid-template-columns: 1fr; align-content: start; }
  .rail { display: none; }
  .form-col { border-left: 0; border-top: 1px solid var(--vp-tool-border); }
  .stage-col { padding: 22px 18px 28px; }
  .form-body { padding: 18px; }
}
</style>
