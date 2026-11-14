// 消息未读数：底栏「消息」上的小粉点用。
//
// 只在客户端取数（服务端渲染时没有会话 cookie 的转发保证，且底栏是纯展示）。
// 多个页面各自挂一份底栏，所以状态放 useState 共享，轮询计时器只留一个。
const POLL_MS = 60_000

import { ApiError } from './useApi'

export function useUnread() {
  const { api } = useApi()
  const count = useState<number>('unread-notifications', () => 0)
  const route = useRoute()

  let timer: ReturnType<typeof setInterval> | null = null
  let mounted = 0

  async function refresh() {
    if (!import.meta.client) return
    try {
      const data = await api<{ unread: number }>('/api/v1/notifications/unread')
      count.value = Number(data.unread) || 0
    } catch (error) {
      // 401 = 没登录 / 刚登出：立刻清零，否则粉点会残留在底栏上。
      // 其它错误（网络抖动、5xx）保持上一次的值，不打扰用户——底栏不该弹错误。
      if (error instanceof ApiError && error.status === 401) count.value = 0
    }
  }

  // 每页挂载时先拉一次（切页回来立刻看到最新），并保证全局只跑一个轮询。
  onMounted(() => {
    mounted += 1
    void refresh()
    if (!timer) timer = setInterval(() => void refresh(), POLL_MS)
  })

  onBeforeUnmount(() => {
    mounted -= 1
    if (mounted <= 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  })

  // 离开消息页（读完消息）后立刻刷新，让粉点及时消失。
  watch(() => route.path, (path, previous) => {
    if (previous && previous.startsWith('/messages') && !path.startsWith('/messages')) {
      void refresh()
    }
  })

  return { count, refresh }
}
