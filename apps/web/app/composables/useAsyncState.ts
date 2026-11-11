// 数据区状态机（UI 规范 §12）：把「加载 / 空 / 错误 / 离线 / 权限不足 / 部分数据」
// 的判定收敛到一处，页面只负责提供请求函数与空态判定。
// 用 useState 避免 SSR 跨请求共享；offline 监听只在客户端注册。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError, errorMessage } from '~/composables/useApi'

export type AsyncPhase = 'ready' | 'loading' | 'empty' | 'error' | 'offline' | 'forbidden'

export function useAsyncState(options: {
  /** 返回 true 表示当前数据为空 */
  isEmpty?: () => boolean
  /** 已有数据时刷新失败：保留内容、显示「部分数据」 */
  keepOnError?: boolean
} = {}) {
  const phase = ref<AsyncPhase>('loading')
  const errorText = ref('')
  const partial = ref(false)
  const retrying = ref(false)
  const hasLoaded = ref(false)
  let onlineHandler: (() => void) | null = null
  let offlineHandler: (() => void) | null = null

  onMounted(() => {
    if (!import.meta.client) return
    onlineHandler = () => {
      // 恢复网络后不自动发请求：由用户点「重试」决定，避免后台标签页惊群。
      if (phase.value === 'offline') phase.value = hasLoaded.value ? 'ready' : 'loading'
    }
    offlineHandler = () => { phase.value = 'offline' }
    window.addEventListener('online', onlineHandler)
    window.addEventListener('offline', offlineHandler)
  })
  onBeforeUnmount(() => {
    if (onlineHandler) window.removeEventListener('online', onlineHandler)
    if (offlineHandler) window.removeEventListener('offline', offlineHandler)
  })

  function fail(error: unknown) {
    errorText.value = errorMessage(error)
    if (error instanceof ApiError && error.status === 401) {
      phase.value = 'forbidden'
      return
    }
    if (import.meta.client && !navigator.onLine) {
      phase.value = 'offline'
      return
    }
    // 已有数据 + keepOnError：不隐藏旧内容，只标注「部分数据」。
    if (options.keepOnError && hasLoaded.value) {
      partial.value = true
      phase.value = 'ready'
      return
    }
    phase.value = 'error'
  }

  function succeed(empty = false) {
    hasLoaded.value = true
    partial.value = false
    errorText.value = ''
    phase.value = empty || (options.isEmpty?.() ?? false) ? 'empty' : 'ready'
  }

  /** 供页面保留自己的 try/catch 时使用：开始一次请求。 */
  function begin() {
    retrying.value = hasLoaded.value || phase.value === 'error' || phase.value === 'offline'
    if (!hasLoaded.value) phase.value = 'loading'
    else partial.value = false
  }

  /** 包装一次请求：自动 begin、统一错误分类。 */
  async function run<T>(request: () => Promise<T>): Promise<T | null> {
    begin()
    try {
      const data = await request()
      succeed()
      return data
    } catch (error) {
      fail(error)
      return null
    } finally {
      retrying.value = false
    }
  }

  return {
    phase,
    errorText,
    partial,
    retrying,
    begin,
    fail,
    succeed,
    run,
    /** 供模板绑定 VpAsyncState 的 state */
    state: computed(() => phase.value),
  }
}
