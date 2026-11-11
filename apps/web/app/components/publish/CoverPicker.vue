<script setup lang="ts">
// 封面选择（UI 规范 §8.1）：候选帧从本地视频用 video + canvas 抽取，
// 不需要等转码完成；另有自定义图片上传入口。选中态以品牌色描边标识，
// 并提供 aria-pressed，不只靠颜色表达状态（§15）。
// 选中的图片只在父组件暂存，真正落库由发布流程在转码完成后提交。
interface Candidate {
  url: string
  blob: Blob
  label: string
}

interface CoverChange {
  blob: Blob | null
  previewUrl: string
}

const props = defineProps<{
  videoFile: File | null
  currentUrl?: string
  disabled?: boolean
}>()
const emit = defineEmits<{
  change: [payload: CoverChange]
  error: [message: string]
}>()

const FRAME_COUNT = 6
const MAX_BYTES = 8 * 1024 * 1024

const candidates = ref<Candidate[]>([])
const selected = ref<number | null>(null) // -1 表示自定义上传
const customPreview = ref('')
const status = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
const statusText = ref('选择视频后，会自动从画面中抽取候选帧。')
const fileInput = ref<HTMLInputElement | null>(null)

let sourceUrl = ''
const objectUrls: string[] = []

function release() {
  for (const url of objectUrls) URL.revokeObjectURL(url)
  objectUrls.length = 0
  if (sourceUrl) {
    URL.revokeObjectURL(sourceUrl)
    sourceUrl = ''
  }
}

function formatTime(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

function toBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error('画面导出失败'))),
      'image/jpeg', 0.86,
    )
  })
}

function seek(video: HTMLVideoElement, seconds: number): Promise<void> {
  // 以 seeked 为准，但必须给超时兜底：部分格式 / 无硬件解码环境里
  // seeked 可能一直不触发，不能让封面选择整体卡死。
  return new Promise((resolve) => {
    let settled = false
    let timer: ReturnType<typeof setTimeout>
    const finish = () => {
      if (settled) return
      settled = true
      video.removeEventListener('seeked', finish)
      clearTimeout(timer)
      resolve()
    }
    timer = setTimeout(finish, 1500)
    video.addEventListener('seeked', finish)
    video.currentTime = seconds
  })
}

async function extract(videoFile: File | null) {
  release()
  candidates.value = []
  customPreview.value = ''
  selected.value = null
  emit('change', { blob: null, previewUrl: '' })
  if (!videoFile) {
    status.value = 'idle'
    statusText.value = '选择视频后，会自动从画面中抽取候选帧。'
    return
  }
  status.value = 'loading'
  statusText.value = '正在从视频抽取候选帧…'
  sourceUrl = URL.createObjectURL(videoFile)
  const video = document.createElement('video')
  video.preload = 'auto'
  video.muted = true
  video.playsInline = true
  video.src = sourceUrl
  try {
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('读取视频超时')), 12000)
      video.onloadedmetadata = () => { clearTimeout(timer); resolve() }
      video.onerror = () => {
        clearTimeout(timer)
        reject(new Error('视频无法解码，可改用自定义上传封面'))
      }
    })
    const duration = video.duration
    if (!Number.isFinite(duration) || duration <= 0) {
      throw new Error('无法读取视频时长，可改用自定义上传封面')
    }
    const scale = Math.min(1, 1280 / Math.max(video.videoWidth, 1))
    const width = Math.max(1, Math.round(video.videoWidth * scale))
    const height = Math.max(1, Math.round(video.videoHeight * scale))
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new Error('浏览器不支持画布抽帧')
    const next: Candidate[] = []
    for (let i = 0; i < FRAME_COUNT; i++) {
      const at = Math.min(duration * (i + 0.5) / FRAME_COUNT, Math.max(duration - 0.05, 0))
      await seek(video, at)
      ctx.drawImage(video, 0, 0, width, height)
      const blob = await toBlob(canvas)
      const url = URL.createObjectURL(blob)
      objectUrls.push(url)
      next.push({ url, blob, label: formatTime(at) })
    }
    candidates.value = next
    status.value = 'ready'
    statusText.value = `已抽取 ${next.length} 个候选帧，点选其一作为封面。`
    emit('error', '')
  } catch (error) {
    status.value = 'error'
    statusText.value = error instanceof Error ? error.message : String(error)
    emit('error', statusText.value)
  }
}

watch(() => props.videoFile, (file) => { void extract(file) })
onBeforeUnmount(release)

function pick(index: number) {
  if (props.disabled) return
  const candidate = candidates.value[index]
  if (!candidate) return
  selected.value = index
  emit('change', { blob: candidate.blob, previewUrl: candidate.url })
}

function onCustomChange(event: Event) {
  if (props.disabled) return
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!file.type.startsWith('image/')) {
    statusText.value = '自定义封面请选择图片文件'
    status.value = 'error'
    emit('error', statusText.value)
    return
  }
  if (file.size > MAX_BYTES) {
    statusText.value = '自定义封面不能超过 8 MiB'
    status.value = 'error'
    emit('error', statusText.value)
    return
  }
  if (customPreview.value) URL.revokeObjectURL(customPreview.value)
  customPreview.value = URL.createObjectURL(file)
  objectUrls.push(customPreview.value)
  selected.value = -1
  status.value = 'ready'
  statusText.value = '已选择自定义封面，发布时会一并提交。'
  emit('error', '')
  emit('change', { blob: file, previewUrl: customPreview.value })
}

function clearSelection() {
  if (props.disabled) return
  selected.value = null
  emit('change', { blob: null, previewUrl: '' })
}

const previewUrl = computed(() => {
  if (selected.value === -1) return customPreview.value
  if (selected.value !== null) return candidates.value[selected.value]?.url || ''
  return props.currentUrl || ''
})

defineExpose({ candidates, selected, status })
</script>

<template>
  <section class="cover-picker" aria-label="封面选择">
    <div class="cover-preview">
      <img v-if="previewUrl" :src="previewUrl" alt="当前封面预览" />
      <span v-else class="cover-empty">封面</span>
    </div>

    <div class="cover-main">
      <div class="cover-head">
        <strong>封面</strong>
        <span :class="{ err: status === 'error' }">{{ statusText }}</span>
      </div>

      <ul v-if="candidates.length" class="frames" aria-label="候选帧">
        <li v-for="(item, index) in candidates" :key="item.url">
          <button
            type="button"
            class="frame"
            :class="{ on: selected === index }"
            :aria-pressed="selected === index"
            :aria-label="`选用 ${item.label} 处的画面作为封面`"
            :disabled="disabled"
            @click="pick(index)"
          >
            <img :src="item.url" :alt="`${item.label} 候选封面`" />
            <small>{{ item.label }}</small>
          </button>
        </li>
      </ul>
      <p v-else-if="status === 'loading'" class="cover-hint" role="status">正在抽帧…</p>
      <p v-else class="cover-hint">选择视频后会出现候选帧横排；也可以直接上传一张图片。</p>

      <div class="cover-actions">
        <label class="custom" :class="{ disabled: !!disabled }">
          <input
            ref="fileInput" type="file" accept="image/jpeg,image/png,image/webp"
            :disabled="disabled" @change="onCustomChange"
          />
          自定义上传封面
        </label>
        <button
          v-if="selected !== null" type="button" class="clear"
          :disabled="disabled" @click="clearSelection"
        >恢复自动封面</button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.cover-picker {
  display: flex; gap: 12px; padding: 12px; margin-top: 14px;
  border: 1px solid var(--vp-tool-border); border-radius: 12px;
  background: var(--vp-tool-surface);
}
.cover-preview {
  width: 84px; height: 112px; flex: none; border-radius: 8px; overflow: hidden;
  background: var(--vp-tool-input); display: grid; place-items: center;
}
.cover-preview img { width: 100%; height: 100%; object-fit: cover; }
.cover-empty { font-size: 11px; color: var(--vp-tool-text-2); }
.cover-main { min-width: 0; flex: 1; display: grid; gap: 8px; align-content: start; }
.cover-head { display: grid; gap: 2px; }
.cover-head strong { font-size: 12px; }
.cover-head span { font-size: 11px; color: var(--vp-tool-text-2); }
.cover-head span.err { color: var(--vp-tool-danger-ink); }
.frames {
  list-style: none; margin: 0; padding: 0; display: flex; gap: 6px;
  overflow-x: auto; scrollbar-width: thin;
}
.frames li { flex: none; }
.frame {
  position: relative; display: grid; gap: 2px; padding: 2px; cursor: pointer;
  border-radius: 7px; border: 2px solid transparent;
  background: var(--vp-tool-surface-2); color: var(--vp-tool-text-2);
}
.frame img { width: 72px; height: 48px; object-fit: cover; border-radius: 4px; display: block; }
.frame small { font-size: 9px; text-align: center; }
.frame.on { border-color: var(--vp-tool-accent); box-shadow: 0 0 0 2px var(--vp-tool-focus); }
.frame:disabled { opacity: .55; cursor: not-allowed; }
.cover-hint { margin: 0; font-size: 11px; line-height: 1.5; color: var(--vp-tool-text-2); }
.cover-actions { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.custom {
  position: relative; display: inline-block; padding: 6px 10px; border-radius: 7px;
  font-size: 11px; cursor: pointer; color: var(--vp-tool-text);
  border: 1px solid var(--vp-tool-border-strong); background: var(--vp-tool-surface-2);
}
.custom.disabled { opacity: .55; cursor: not-allowed; }
.custom input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
.clear {
  padding: 5px 8px; font-size: 11px; border-radius: 7px; cursor: pointer;
  border: 1px solid transparent; background: transparent; color: var(--vp-tool-text-2);
}
.clear:disabled { opacity: .55; cursor: not-allowed; }
</style>
