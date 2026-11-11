// 播放进度上报（§9.1）：播放中每 10 秒批量补报一次；
// 页面隐藏 / 关闭时用 keepalive 立即补报，避免丢最后一次位置。
// 未登录时接口 401，静默忽略（进度是登录用户的增值能力）。
import { onBeforeUnmount, onMounted } from 'vue'
import type { useImmersivePlayer } from './useImmersivePlayer'

export function useProgressReporter(
  player: ReturnType<typeof useImmersivePlayer>,
  getContent: () => { id: number; type: string } | null,
) {
  const { api } = useApi()
  let timer: ReturnType<typeof setInterval> | null = null
  let inFlight = false

  function send(): void {
    const content = getContent()
    if (!content || content.type !== 'video' || !player.state.ready || inFlight) return
    const positionMS = Math.round((player.state.currentTime || 0) * 1000)
    const durationMS = Math.round((player.state.duration || 0) * 1000)
    if (positionMS < 1000) return
    inFlight = true
    void api(`/api/v1/contents/${content.id}/progress`, {
      method: 'POST',
      body: { position_ms: positionMS, duration_ms: durationMS },
    }).catch(() => { /* 匿名或网络失败：进度是可丢失数据 */ }).finally(() => {
      inFlight = false
    })
  }

  function start() {
    if (timer) return
    timer = setInterval(send, 10_000)
  }

  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  function onVisibility() {
    if (document.visibilityState === 'hidden') send()
  }

  onMounted(() => {
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('pagehide', send)
  })
  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', onVisibility)
    window.removeEventListener('pagehide', send)
    send()
    stop()
  })

  return { start, stop, send }
}
