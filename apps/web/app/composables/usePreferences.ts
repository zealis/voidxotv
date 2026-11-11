// 全局用户偏好：设置页写入，播放器与动效在别处消费。
// 用 useState 而非模块级变量，避免 SSR 期间跨请求共享状态。
export type ToolTheme = 'system' | 'light' | 'dark'

export interface UserPreferences {
  loaded: boolean
  autoplay: boolean
  reduceMotion: boolean
  defaultVisibility: 'public' | 'friends' | 'private'
  theme: ToolTheme
}

const STORAGE_KEY = 'yj-tool-theme'
const VALID_THEMES: ToolTheme[] = ['system', 'light', 'dark']

// 系统主题监听器是浏览器全局单例：usePreferences() 会在多个组件里被调用，
// 若挂在每个调用的闭包上，会重复 addEventListener 且无法统一卸载。
let systemQuery: MediaQueryList | null = null
let onSystemChange: (() => void) | null = null

export function isToolTheme(value: unknown): value is ToolTheme {
  return typeof value === 'string' && (VALID_THEMES as string[]).includes(value)
}

// 工具型页面路径（§11.1）：沉浸式页面一律恒深色，浏览器 UI 也不跟随主题。
function isToolPage(): boolean {
  if (!import.meta.client) return false
  return /^\/(settings|studio)(\/|\?|$)/.test(window.location.pathname + window.location.search)
}

export function usePreferences() {
  const preferences = useState<UserPreferences>('user-preferences', () => ({
    loaded: false,
    autoplay: true,
    reduceMotion: false,
    defaultVisibility: 'public',
    theme: 'system',
  }))
  const { api, getMe } = useApi()

  // 减少动效是「通用」分组里唯一需要落到 DOM 的偏好：加在 html 上，
  // 由 app.vue 的全局规则把过渡 / 动画压到近乎瞬时。
  function applyMotion() {
    if (!import.meta.client) return
    document.documentElement.classList.toggle('vp-reduce-motion', preferences.value.reduceMotion)
  }

  // 工具页主题（§14）：html 属性只被 [data-surface="tool"] 消费。
  // system 时跟随 prefers-color-scheme；显式 light / dark 优先。
  function resolveTheme(theme: ToolTheme): 'light' | 'dark' {
    if (theme === 'light') return 'light'
    if (theme === 'dark') return 'dark'
    return import.meta.client && window.matchMedia('(prefers-color-scheme: light)').matches
      ? 'light'
      : 'dark'
  }

  function applyTheme() {
    if (!import.meta.client) return
    const resolved = resolveTheme(preferences.value.theme)
    document.documentElement.setAttribute('data-tool-theme', resolved)
    // 主题切换只换 CSS 变量，不重排、不重载，避免闪烁。
    // theme-color 只在工具页改：沉浸页的浏览器 UI 必须保持深色基线。
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) {
      meta.setAttribute('content',
        isToolPage() && resolved === 'light' ? '#f4f5f9' : '#0a0a1f')
    }
  }

  // 用户显式选择：先写本地（下次首屏脚本就能读到，不会闪一下再跳），再由调用方持久化到服务端。
  function setTheme(theme: ToolTheme) {
    if (!isToolTheme(theme)) return
    preferences.value.theme = theme
    if (import.meta.client) {
      try { localStorage.setItem(STORAGE_KEY, theme) } catch { /* 隐私模式下忽略 */ }
    }
    applyTheme()
    watchSystemTheme()
  }

  // system 模式下系统主题变化要实时跟随；显式选择后不需要继续监听。
  function watchSystemTheme() {
    if (!import.meta.client) return
    if (onSystemChange && systemQuery) {
      systemQuery.removeEventListener('change', onSystemChange)
      onSystemChange = null
      systemQuery = null
    }
    if (preferences.value.theme !== 'system') return
    systemQuery = window.matchMedia('(prefers-color-scheme: light)')
    onSystemChange = () => applyTheme()
    systemQuery.addEventListener('change', onSystemChange)
  }

  // 服务端是权威值：登录后覆盖本地缓存（例如换设备、或另一台设备刚改过）。
  function adoptTheme(value: unknown) {
    const theme: ToolTheme = isToolTheme(value) ? value : 'system'
    preferences.value.theme = theme
    if (import.meta.client) {
      try { localStorage.setItem(STORAGE_KEY, theme) } catch { /* 隐私模式下忽略 */ }
    }
    applyTheme()
    watchSystemTheme()
  }

  async function load(force = false) {
    if (preferences.value.loaded && !force) return preferences.value
    if (!(await getMe())) return preferences.value
    try {
      const data = await api<{
        autoplay: boolean
        reduce_motion: boolean
        default_visibility: UserPreferences['defaultVisibility']
        theme: ToolTheme
      }>('/api/v1/me/settings')
      preferences.value = {
        loaded: true,
        autoplay: data.autoplay,
        reduceMotion: data.reduce_motion,
        defaultVisibility: data.default_visibility,
        theme: data.theme,
      }
      applyMotion()
      adoptTheme(data.theme)
    } catch {
      // 未登录或接口暂时不可用时保持默认值，不打断内容浏览。
    }
    return preferences.value
  }

  return { preferences, load, applyMotion, applyTheme, setTheme, adoptTheme, resolveTheme }
}
