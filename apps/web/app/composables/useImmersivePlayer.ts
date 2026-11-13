// 沉浸流媒体实例池（《前端工程架构规范》§8.2）。
//
// 与 §8.1「单例复用」的差异（有意修订，见 CHANGELOG）：
//   规范原本只要求保留 ±1 的媒体资源，因此单例加一次 clearSource() 就够。
//   现在窗口放大到「向下 3 / 向上 5」，单例无法同时缓冲多条，所以改成
//   **有上限的实例池**：窗口内每条一个 <video>（各自独立的 HLS 实例与缓冲），
//   超出窗口立即释放。仍然不是「每条内容一个长期实例」——池大小由窗口决定，
//   且同一时刻只有 active 条目在播放与发声。
//
// 关键行为：
//   - 切走：pause + 记住 currentTime + hls.stopLoad()，元素退回隐藏池，缓冲保留；
//   - 切回：hls.startLoad(记住的位置) 并从该位置续播，命中缓冲则秒开；
//   - 预载：静音、只缓冲头部（WARM_BUFFER_SECONDS），不参与播放；
//   - 超出窗口：destroy HLS / revoke Blob / 卸载 src / 移除元素。
import type HlsType from 'hls.js'
import type { FeedItem, FeedMedia } from './useFeed'

// 预载态只缓冲头部：够秒开即可，避免窗口内 8 条一起下完整视频。
const WARM_BUFFER_SECONDS = 6
// 播放态沿用原来的前向缓冲配置。
const PLAY_BUFFER_SECONDS = 12
const PLAY_MAX_BUFFER_SECONDS = 24
// 后向缓冲上限：回滑依赖它，但也不能无限涨（一条 1080p 每分钟约 8–15MB）。
const BACK_BUFFER_SECONDS = 30
// 同时进行的预载数量：多了会和当前条目抢带宽，反而拖慢首帧。
const WARM_CONCURRENCY = 2

type Level = { index: number; label: string }

interface Entry {
  slug: string
  item: FeedItem
  video: HTMLVideoElement
  hls: HlsType | null
  blobURL: string | null
  variantBlobs: string[]
  usingHEVC: boolean
  fallbackURL: string
  // 是否已经挂过 source（决定切回时是「续播」还是「新建加载」）。
  loaded: boolean
  failed: boolean
  ready: boolean
  loading: boolean
  duration: number
  videoWidth: number
  videoHeight: number
  levels: Level[]
  level: number
  message: string
  // 离开时的播放位置，切回从这里续播。
  savedTime: number
}

export interface MediaWindow {
  /** 向下预载条数 */
  forward: number
  /** 向上保留条数 */
  backward: number
}

export interface PlayerOptions {
  /**
   * 列表接口只给签名摘要，HLS 的 master playlist 只有详情接口才有。
   * 预载窗口内的条目时用这个回调补齐完整 media。
   */
  resolveMedia?: (item: FeedItem) => Promise<FeedMedia>
}

export function useImmersivePlayer(options: PlayerOptions = {}) {
  const pool = new Map<string, Entry>()
  let activeSlug = ''
  let host: HTMLElement | null = null
  let autoplay = true
  let hevcSupport: boolean | null = null

  // 预载队列：限制并发，失败静默（用户还没滑到它）。
  const warmQueue: FeedItem[] = []
  let warming = 0
  const warmingSlugs = new Set<string>()

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
    rate: 1,
    volume: 1,
    levels: [] as Level[],
    level: -1,
    videoWidth: 0,
    videoHeight: 0,
  })

  // 池的可观测快照，供验收脚本与调试面板读取。
  const poolInfo = reactive({ slugs: [] as string[], active: '' })

  function syncPoolInfo(): void {
    poolInfo.slugs = [...pool.keys()]
    poolInfo.active = activeSlug
  }

  // ── 隐藏池容器 ────────────────────────────────────────────────
  // 预载中的 <video> 必须留在视口内才会真正解码缓冲，所以用 fixed + 1px 可见度
  // 藏在内容层下面，而不是 display:none / 移出 DOM（那样会被浏览器节流甚至不加载）。
  function ensureHost(): HTMLElement {
    if (host) return host
    const element = document.createElement('div')
    element.id = 'yj-media-pool'
    element.setAttribute('aria-hidden', 'true')
    const style = element.style
    style.position = 'fixed'
    style.left = '0'
    style.top = '0'
    style.width = '320px'
    style.height = '180px'
    style.overflow = 'hidden'
    style.opacity = '0.01'
    style.pointerEvents = 'none'
    style.zIndex = '-1'
    document.body.appendChild(element)
    host = element
    return element
  }

  // ── 缓冲区策略 ────────────────────────────────────────────────
  function applyBufferMode(entry: Entry, active: boolean): void {
    const hls = entry.hls
    if (!hls) return
    hls.config.maxBufferLength = active ? PLAY_BUFFER_SECONDS : WARM_BUFFER_SECONDS
    hls.config.maxMaxBufferLength = active ? PLAY_MAX_BUFFER_SECONDS : WARM_BUFFER_SECONDS
  }

  // ── 状态镜像 ──────────────────────────────────────────────────
  function snapshotToState(entry: Entry): void {
    state.ready = entry.ready
    state.failed = entry.failed
    state.loading = entry.loading
    state.message = entry.message
    state.duration = entry.duration
    state.videoWidth = entry.videoWidth
    state.videoHeight = entry.videoHeight
    state.levels = entry.levels
    state.level = entry.level
    state.currentTime = Number.isFinite(entry.video.currentTime) ? entry.video.currentTime : 0
    state.playing = !entry.video.paused && entry.ready
    state.ended = false
  }

  // ── 探测 ──────────────────────────────────────────────────────
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

  // ── 实例创建 ──────────────────────────────────────────────────
  function ensureEntry(item: FeedItem): Entry {
    const existing = pool.get(item.slug)
    if (existing) return existing

    const video = document.createElement('video')
    video.className = 'feed-video'
    video.setAttribute('playsinline', '')
    video.setAttribute('webkit-playsinline', '')
    // 预载态用 auto：要靠它把头部数据真正拉下来；播放态也不需要改回去。
    video.setAttribute('preload', 'auto')
    video.muted = true
    video.loop = true

    let entry!: Entry
    const isActive = () => entry.slug === activeSlug

    video.addEventListener('play', () => {
      entry.ready = true
      if (isActive()) {
        state.playing = true
        state.ended = false
      }
    })
    video.addEventListener('pause', () => {
      if (isActive()) state.playing = false
    })
    video.addEventListener('ended', () => {
      if (isActive()) {
        state.playing = false
        state.ended = true
      }
    })
    video.addEventListener('timeupdate', () => {
      if (!isActive()) return
      state.currentTime = video.currentTime || 0
    })
    video.addEventListener('loadedmetadata', () => {
      entry.duration = Number.isFinite(video.duration) ? video.duration : 0
      entry.videoWidth = video.videoWidth
      entry.videoHeight = video.videoHeight
      if (isActive()) {
        state.duration = entry.duration
        state.videoWidth = entry.videoWidth
        state.videoHeight = entry.videoHeight
      }
    })
    video.addEventListener('error', () => {
      if (!isActive()) {
        entry.failed = true
        return
      }
      // HEVC 解码失败静默回退 H.264，不让用户看到「播放失败」。
      if (entry.usingHEVC && entry.fallbackURL) {
        entry.usingHEVC = false
        video.src = entry.fallbackURL
        void video.play().catch(() => { state.playing = false })
        return
      }
      entry.failed = true
      entry.loading = false
      state.failed = true
      state.loading = false
    })

    entry = {
      slug: item.slug,
      item,
      video,
      hls: null,
      blobURL: null,
      variantBlobs: [],
      usingHEVC: false,
      fallbackURL: '',
      loaded: false,
      failed: false,
      ready: false,
      loading: false,
      duration: 0,
      videoWidth: 0,
      videoHeight: 0,
      levels: [],
      level: -1,
      message: '',
      savedTime: 0,
    }
    pool.set(item.slug, entry)
    ensureHost().appendChild(video)
    syncPoolInfo()
    return entry
  }

  function release(slug: string): void {
    const entry = pool.get(slug)
    if (!entry) return
    if (entry.hls) {
      entry.hls.destroy()
      entry.hls = null
    }
    if (entry.blobURL) {
      URL.revokeObjectURL(entry.blobURL)
      entry.blobURL = null
    }
    for (const url of entry.variantBlobs) URL.revokeObjectURL(url)
    entry.variantBlobs = []
    entry.video.pause()
    entry.video.removeAttribute('src')
    entry.video.load()
    entry.video.remove()
    pool.delete(slug)
    warmingSlugs.delete(slug)
    syncPoolInfo()
  }

  // ── 挂载 ──────────────────────────────────────────────────────
  // 只记录目标槽位，真正搬运放到 load() 里：调用方总是 attach(slot) → load(item)，
  // 而 attach 时 activeSlug 还是上一条，提前搬运会把旧画面塞进新条目的槽。
  let slotContainer: HTMLElement | null = null

  function attach(container: HTMLElement | null): void {
    slotContainer = container
  }

  // ── 加载 source（新建 / 重建） ─────────────────────────────────
  async function loadSource(entry: Entry, item: FeedItem): Promise<void> {
    const video = entry.video
    const media = item.media

    entry.loaded = true
    entry.failed = false
    entry.ready = false
    entry.loading = true
    entry.message = ''
    entry.savedTime = 0
    const isActive = entry.slug === activeSlug
    if (isActive) {
      state.failed = false
      state.ready = false
      state.loading = true
      state.message = ''
      state.currentTime = 0
      state.duration = 0
    }

    if (item.type === 'image' || media.mode === 'image') {
      entry.loading = false
      entry.ready = false
      if (isActive) state.loading = false
      return
    }

    if (media.mode === 'hls' && media.master_playlist) {
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
            entry.variantBlobs.push(url)
            return url
          }
          // 变体不在后端重写清单里：保留原行只会拉到坏档位，直接丢弃。
          if (trimmed.endsWith('.m3u8')) return ''
          return line
        }).filter((line) => line !== '').join('\n')
      }
      entry.blobURL = URL.createObjectURL(
        new Blob([masterText], { type: 'application/vnd.apple.mpegurl' }),
      )
      try {
        const module = await import('hls.js')
        const Hls = module.default
        if (Hls.isSupported()) {
          const hls = new Hls({
            enableWorker: true,
            maxBufferLength: WARM_BUFFER_SECONDS,
            maxMaxBufferLength: WARM_BUFFER_SECONDS,
            backBufferLength: BACK_BUFFER_SECONDS,
            capLevelToPlayerSize: true,
          })
          entry.hls = hls
          hls.on(Hls.Events.MANIFEST_PARSED, () => {
            entry.ready = true
            entry.loading = false
            entry.levels = hls.levels.map((level, index) => {
              const shortSide = Math.min(level.width || 0, level.height || 0)
              return {
                index,
                label: shortSide ? `${shortSide}p` : `${Math.round(level.bitrate / 1000)} kbps`,
              }
            })
            entry.level = hls.currentLevel
            video.playbackRate = state.rate
            if (entry.slug === activeSlug) {
              applyBufferMode(entry, true)
              snapshotToState(entry)
              if (autoplay) void video.play().catch(() => { state.playing = false })
            } else {
              // 预载实例：清单解析完即暂停，等用户滑到再播。
              video.pause()
            }
          })
          hls.on(Hls.Events.ERROR, (_event, data) => {
            if (!data?.fatal) return
            entry.failed = true
            entry.loading = false
            entry.message = '播放失败，点击重试'
            if (entry.slug === activeSlug) {
              state.failed = true
              state.loading = false
              state.message = entry.message
            }
          })
          hls.loadSource(entry.blobURL)
          hls.attachMedia(video)
        } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
          video.src = media.media_url || ''
          entry.ready = true
          entry.loading = false
          if (entry.slug === activeSlug && autoplay) void video.play().catch(() => {})
        } else {
          entry.failed = true
          entry.loading = false
          entry.message = '当前浏览器不支持 HLS'
          if (entry.slug === activeSlug) {
            state.failed = true
            state.loading = false
            state.message = entry.message
          }
        }
      } catch {
        entry.failed = true
        entry.loading = false
        entry.message = '播放器加载失败'
        if (entry.slug === activeSlug) {
          state.failed = true
          state.loading = false
          state.message = entry.message
        }
      }
      return
    }

    if (media.media_url) {
      entry.usingHEVC = Boolean(media.hevc_url) && canPlayHEVC()
      entry.fallbackURL = media.media_url
      video.src = entry.usingHEVC ? media.hevc_url! : media.media_url
      entry.ready = true
      entry.loading = false
      if (entry.slug === activeSlug) {
        state.ready = true
        state.loading = false
        if (autoplay) void video.play().catch(() => { state.playing = false })
      } else {
        video.pause()
      }
      return
    }

    entry.failed = true
    entry.loading = false
    entry.message = '媒体尚未就绪'
    if (entry.slug === activeSlug) {
      state.failed = true
      state.loading = false
      state.message = entry.message
    }
  }

  // 预载窗口内某条：补齐 media → 建实例 → 缓冲头部。
  async function warm(item: FeedItem): Promise<void> {
    if (item.type === 'image' || item.media.mode === 'image') return
    if (pool.has(item.slug)) return
    if (warmingSlugs.has(item.slug)) return
    warmingSlugs.add(item.slug)
    warmQueue.push(item)
    pumpWarm()
  }

  function pumpWarm(): void {
    if (warming >= WARM_CONCURRENCY) return
    const next = warmQueue.shift()
    if (!next) return
    if (next.slug === activeSlug || pool.has(next.slug)) {
      warmingSlugs.delete(next.slug)
      pumpWarm()
      return
    }
    warming += 1
    void (async () => {
      try {
        // 列表接口只有签名摘要：HLS 缺 master playlist 时补一次详情。
        if (next.media.mode === 'hls' && !next.media.master_playlist && options.resolveMedia) {
          const media = await options.resolveMedia(next)
          next.media = media
        }
        if (!next.media.media_url && !next.media.master_playlist) return
        const entry = ensureEntry(next)
        await loadSource(entry, next)
      } catch {
        // 预载失败不打扰用户：真正滑到时 load() 会重新尝试。
      } finally {
        warming = Math.max(0, warming - 1)
        warmingSlugs.delete(next.slug)
        pumpWarm()
      }
    })()
  }

  // ── 窗口维护 ──────────────────────────────────────────────────
  /**
   * 按「当前条目 + 向下 forward + 向上 backward」预载并裁剪实例池。
   * 每次切换条目后调用一次即可；弱网下调小窗口由调用方决定。
   */
  function prepare(items: FeedItem[], index: number, window: MediaWindow): void {
    const keep = new Set<string>()
    const from = index - Math.max(0, window.backward)
    const to = index + Math.max(0, window.forward)
    for (let i = from; i <= to; i += 1) {
      const item = items[i]
      if (!item) continue
      keep.add(item.slug)
      if (i !== index) void warm(item)
    }
    for (const slug of [...pool.keys()]) {
      if (!keep.has(slug)) release(slug)
    }
    // 队列里已被移出窗口的条目也一并丢弃，省得白占预载名额。
    for (let i = warmQueue.length - 1; i >= 0; i -= 1) {
      if (!keep.has(warmQueue[i]!.slug)) {
        warmingSlugs.delete(warmQueue[i]!.slug)
        warmQueue.splice(i, 1)
      }
    }
  }

  // ── 切换当前条目 ──────────────────────────────────────────────
  async function load(item: FeedItem): Promise<void> {
    const previous = activeSlug ? pool.get(activeSlug) : undefined
    if (previous && previous.slug !== item.slug) {
      // 记住位置：这里不做任何卸载，缓冲原样留着等用户滑回来。
      previous.savedTime = Number.isFinite(previous.video.currentTime) ? previous.video.currentTime : 0
      previous.video.pause()
      previous.video.muted = true
      previous.hls?.stopLoad()
      ensureHost().appendChild(previous.video)
    }

    // 图集没有媒体实例：清空 active 并归还池位，不为图片创建空 <video>。
    if (item.type === 'image' || item.media.mode === 'image') {
      activeSlug = ''
      syncPoolInfo()
      state.ready = false
      state.loading = false
      state.failed = false
      state.playing = false
      state.ended = false
      state.duration = 0
      state.currentTime = 0
      state.message = ''
      return
    }

    const entry = ensureEntry(item)
    activeSlug = item.slug
    syncPoolInfo()
    snapshotToState(entry)
    // 把这一条的 <video> 从隐藏池搬进当前条目的媒体槽（不是 active 的实例留在池里继续缓冲）。
    if (slotContainer && entry.video.parentElement !== slotContainer) {
      slotContainer.appendChild(entry.video)
    }

    // 已有缓冲：直接从离开的位置续播，不再重新拉流。
    if (entry.loaded && !entry.failed && (entry.ready || entry.hls)) {
      applyBufferMode(entry, true)
      if (entry.hls) {
        entry.hls.startLoad(entry.savedTime > 0 ? entry.savedTime : -1)
      }
      if (entry.savedTime > 0) {
        try { entry.video.currentTime = entry.savedTime } catch { /* 元数据未就绪时忽略 */ }
      }
      entry.video.muted = state.muted
      entry.video.playbackRate = state.rate
      entry.video.volume = state.volume
      if (autoplay) {
        try { await entry.video.play() } catch { state.playing = false }
      }
      snapshotToState(entry)
      return
    }

    await loadSource(entry, item)
    // 用户此前手动取消过静音时，新条目不应退回静音。
    entry.video.muted = state.muted
    entry.video.volume = state.volume
  }

  // ── 播放控制（作用于当前条目） ────────────────────────────────
  function activeEntry(): Entry | null {
    return activeSlug ? pool.get(activeSlug) ?? null : null
  }

  async function toggle(): Promise<void> {
    const entry = activeEntry()
    if (!entry) return
    if (entry.video.paused) {
      try { await entry.video.play() } catch { state.playing = false }
    } else {
      entry.video.pause()
    }
  }

  function pause(): void {
    activeEntry()?.video.pause()
  }

  async function resume(): Promise<void> {
    const entry = activeEntry()
    if (!entry) return
    try { await entry.video.play() } catch { state.playing = false }
  }

  function setMuted(value: boolean): void {
    state.muted = value
    const entry = activeEntry()
    if (entry) entry.video.muted = value
  }

  function setRate(rate: number): void {
    state.rate = rate
    const entry = activeEntry()
    if (entry) entry.video.playbackRate = rate
  }

  function setLevel(index: number): void {
    const entry = activeEntry()
    if (!entry?.hls) return
    entry.hls.currentLevel = index
    entry.level = index
    state.level = index
  }

  function setVolume(value: number): void {
    state.volume = value
    const entry = activeEntry()
    if (!entry) return
    const video = entry.video
    video.volume = Math.min(Math.max(value, 0), 1)
    // 拉动音量即视为解除静音，否则滑块没有可感知的效果。
    if (value > 0 && video.muted) {
      video.muted = false
      state.muted = false
    }
  }

  function seekRatio(ratio: number): void {
    const entry = activeEntry()
    if (!entry || !state.duration) return
    entry.video.currentTime = Math.min(Math.max(ratio, 0), 1) * state.duration
  }

  async function requestPiP(): Promise<void> {
    const entry = activeEntry()
    if (!entry) return
    if (document.pictureInPictureElement === entry.video) {
      await document.exitPictureInPicture().catch(() => {})
      return
    }
    await entry.video.requestPictureInPicture().catch(() => {})
  }

  async function toggleFullscreen(): Promise<void> {
    const entry = activeEntry()
    if (!entry) return
    try {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await entry.video.requestFullscreen()
    } catch { /* unsupported: ignore */ }
  }

  function setAutoplay(value: boolean) {
    autoplay = value
    if (!value) pause()
  }

  function destroy(): void {
    for (const slug of [...pool.keys()]) release(slug)
    activeSlug = ''
    warmQueue.length = 0
    warmingSlugs.clear()
    host?.remove()
    host = null
    syncPoolInfo()
  }

  return {
    state, poolInfo, attach, load, prepare, toggle, pause, resume, setMuted, setAutoplay,
    seekRatio, toggleFullscreen, destroy, setRate, setLevel, setVolume, requestPiP,
  }
}
