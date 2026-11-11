// 单例播放器：整个沉浸流只存在一个 <video> 元素，切条时把它移动到当前条目的媒体槽。
// HLS 使用后端重写过绝对签名地址的 master playlist（Blob URL 交给 hls.js）；
// 下一条只预载封面，不同时下载多条完整视频（UI 规范 §5.2）。
import type HlsType from 'hls.js'
import type { FeedItem } from './useFeed'

export function useImmersivePlayer() {
  let video: HTMLVideoElement | null = null
  let hls: HlsType | null = null
  let blobURL: string | null = null
  // 变体播放清单的 Blob URL：签名模式下 master 里的变体行要替换成它们，
  // 否则 hls.js 从存储直拉变体，相对分片引用不继承签名（403）。
  let variantBlobs: string[] = []
  let currentSlug = ''
  // HEVC 自动选流状态：能力探测结果缓存一次；回退地址用于解码失败时退回 H.264。
  let hevcSupport: boolean | null = null
  let usingHEVC = false
  let fallbackURL = ''

  // 滚动自动播放偏好：关闭后加载完不自动起播，由用户点按开始。
  let autoplay = true

  const state = reactive({
    playing: false,
    ended: false,
    muted: true,
    loading: false,
    ready: false,
    failed: false,
    currentTime: 0,
    duration: 0,
    message: '',
    // 播放设置（§6.4 悬浮控件）：倍速 / 清晰度 / 音量。
    rate: 1,
    volume: 1,
    levels: [] as { index: number; label: string }[],
    level: -1, // -1 = 自动（HLS ABR）
    // 源视频尺寸：驱动沉浸流的 contain/cover 自适应——横屏源 cover 裁两侧
    // 消除黑边；竖屏源 contain 保留完整画面（桌面横屏显示器上竖屏源 cover
    // 会裁掉大半，不可接受）。
    videoWidth: 0,
    videoHeight: 0,
  })

  function ensureVideo(): HTMLVideoElement {
    if (video) return video
    const element = document.createElement('video')
    element.className = 'feed-video'
    element.setAttribute('playsinline', '')
    element.setAttribute('webkit-playsinline', '')
    element.setAttribute('preload', 'metadata')
    element.muted = true
    element.loop = true
    element.addEventListener('play', () => { state.playing = true })
    element.addEventListener('ended', () => { state.playing = false; state.ended = true })
    element.addEventListener('pause', () => { state.playing = false })
    element.addEventListener('timeupdate', () => {
      state.currentTime = element.currentTime || 0
    })
    element.addEventListener('loadedmetadata', () => {
      state.duration = Number.isFinite(element.duration) ? element.duration : 0
      state.videoWidth = element.videoWidth
      state.videoHeight = element.videoHeight
    })
	    element.addEventListener('error', () => {
	      if (!currentSlug) return
	      // HEVC 解码失败时静默回退到 H.264，不让用户看到“播放失败”。
	      if (usingHEVC && fallbackURL) {
	        usingHEVC = false
	        element.src = fallbackURL
	        void element.play().catch(() => { state.playing = false })
	        return
	      }
	      state.failed = true
	    })
    video = element
    return element
  }

  function attach(container: HTMLElement | null): void {
    if (!container) return
    const element = ensureVideo()
    if (element.parentElement !== container) container.appendChild(element)
  }

  function clearSource(): void {
    const element = video
    usingHEVC = false
    fallbackURL = ''
    if (hls) {
      hls.destroy()
      hls = null
    }
    if (blobURL) {
      URL.revokeObjectURL(blobURL)
      blobURL = null
    }
    for (const url of variantBlobs) URL.revokeObjectURL(url)
    variantBlobs = []
    if (element) {
      element.pause()
      element.removeAttribute('src')
      element.load()
    }
  }

  // HEVC 能力探测（§5.1）：只有浏览器明确声明支持 hvc1 才切换到可选源。
  // Chrome/Firefox 桌面版通常返回空字符串，自动继续用 H.264。
  function canPlayHEVC(): boolean {
    if (hevcSupport !== null) return hevcSupport
    try {
      const probe = document.createElement('video')
      const result = probe.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"')
      hevcSupport = result === 'probably' || result === 'maybe'
    } catch {
      hevcSupport = false
    }
    return hevcSupport
  }

  async function load(item: FeedItem): Promise<void> {
    const element = ensureVideo()
    currentSlug = item.slug
    clearSource()
    state.failed = false
    state.ready = false
    state.ended = false
    state.loading = true
    state.currentTime = 0
    state.duration = 0

    if (item.type === 'image' || item.media.mode === 'image') {
      state.loading = false
      return
    }

    const media = item.media
    if (media.mode === 'hls' && media.master_playlist) {
      // 变体行替换成 Blob URL：变体文本来自后端重写（分片已带签名）。
      const playlists = media.playlists || {}
      let masterText = media.master_playlist
      if (Object.keys(playlists).length) {
        const entries = Object.entries(playlists)
        masterText = masterText.split('\n').map((line) => {
          const trimmed = line.trim()
          if (!trimmed || trimmed.startsWith('#')) return line
          const hit = entries.find(([key]) => decodeURIComponent(trimmed).includes(key))
          if (hit) {
            const url = URL.createObjectURL(new Blob([hit[1]], { type: 'application/vnd.apple.mpegurl' }))
            variantBlobs.push(url)
            return url
          }
          // 变体不在后端重写清单里（未登记的历史 / 导入数据）：没有可用的带签名
          // 分片，保留原行只会让 hls.js 拉到坏档位，直接丢弃该档。
          if (trimmed.endsWith('.m3u8')) return ''
          return line
        }).filter((line) => line !== '').join('\n')
      }
      blobURL = URL.createObjectURL(
        new Blob([masterText], { type: 'application/vnd.apple.mpegurl' }),
      )
      try {
        const module = await import('hls.js')
        const Hls = module.default
        if (Hls.isSupported()) {
          hls = new Hls({
            enableWorker: true,
            maxBufferLength: 12,
            maxMaxBufferLength: 24,
            capLevelToPlayerSize: true,
          })
          hls.on(Hls.Events.MANIFEST_PARSED, () => {
            state.ready = true
            state.loading = false
            refreshLevels()
            element.playbackRate = state.rate
            if (autoplay) void element.play().catch(() => { state.playing = false })
          })
          hls.on(Hls.Events.ERROR, (_event, data) => {
            if (data?.fatal) {
              state.failed = true
              state.loading = false
              state.message = '播放失败，点击重试'
            }
          })
          hls.loadSource(blobURL)
          hls.attachMedia(element)
        } else if (element.canPlayType('application/vnd.apple.mpegurl')) {
          element.src = media.media_url || ''
          state.ready = true
          state.loading = false
          if (autoplay) void element.play().catch(() => {})
        } else {
          state.failed = true
          state.loading = false
          state.message = '当前浏览器不支持 HLS'
        }
      } catch {
        state.failed = true
        state.loading = false
        state.message = '播放器加载失败'
      }
      return
    }

    if (media.media_url) {
      // 支持 HEVC 时优先体积更小的 mp4_hevc，否则保持 H.264。
      usingHEVC = Boolean(media.hevc_url) && canPlayHEVC()
      fallbackURL = media.media_url
      element.src = usingHEVC ? media.hevc_url! : media.media_url
      state.ready = true
      state.loading = false
      if (autoplay) void element.play().catch(() => { state.playing = false })
      return
    }
    state.failed = true
    state.loading = false
    state.message = '媒体尚未就绪'
  }

  async function toggle(): Promise<void> {
    const element = video
    if (!element) return
    if (element.paused) {
      try { await element.play() } catch { state.playing = false }
    } else {
      element.pause()
    }
  }

  function pause(): void {
    video?.pause()
  }

  async function resume(): Promise<void> {
    if (!video) return
    try { await video.play() } catch { state.playing = false }
  }

  function setMuted(value: boolean): void {
    state.muted = value
    if (video) video.muted = value
  }

  // ── 播放设置（§6.4）────────────────────────────────────────────

  function refreshLevels(): void {
    if (!hls) {
      state.levels = []
      return
    }
    // 竖屏视频的 level.height 是长边（如 720x1280 → "1280p"）；行业惯例按短边标注。
    state.levels = hls.levels.map((level, index) => {
      const shortSide = Math.min(level.width || 0, level.height || 0)
      return {
        index,
        label: shortSide ? `${shortSide}p` : `${Math.round(level.bitrate / 1000)} kbps`,
      }
    })
    state.level = hls.currentLevel
  }

  function setRate(rate: number): void {
    state.rate = rate
    if (video) video.playbackRate = rate
  }

  function setLevel(index: number): void {
    if (!hls) return
    hls.currentLevel = index
    state.level = index
  }

  function setVolume(value: number): void {
    state.volume = value
    if (!video) return
    video.volume = Math.min(Math.max(value, 0), 1)
    // 拉动音量即视为解除静音，否则滑块没有可感知的效果。
    if (value > 0 && video.muted) {
      video.muted = false
      state.muted = false
    }
  }

  function seekRatio(ratio: number): void {
    if (!video || !state.duration) return
    video.currentTime = Math.min(Math.max(ratio, 0), 1) * state.duration
  }

  async function requestPiP(): Promise<void> {
    const video = ensureVideo()
    if (document.pictureInPictureElement === video) {
      await document.exitPictureInPicture().catch(() => {})
      return
    }
    await video.requestPictureInPicture().catch(() => {})
  }

  async function toggleFullscreen(): Promise<void> {
    const element = video
    if (!element) return
    try {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await element.requestFullscreen()
    } catch { /* unsupported: ignore */ }
  }

  function destroy(): void {
    clearSource()
    if (video) {
      video.remove()
      video = null
    }
    currentSlug = ''
  }

  function setAutoplay(value: boolean) {
    autoplay = value
    if (!value) pause()
  }

  return {
    state, attach, load, toggle, pause, resume, setMuted, setAutoplay,
    seekRatio, toggleFullscreen, destroy, setRate, setLevel, setVolume, requestPiP,
  }
}
