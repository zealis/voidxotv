// 播放器方向键快捷键：左右 ±5 秒定位，上下 ±5% 音量。
// 首页沉浸流与视频详情页共用这一份实现，避免两处行为漂移。
// 只在拿到视频时长时生效；图片 / 还未加载出时长时忽略，避免抢走页面滚动。

const SEEK_STEP_SECONDS = 5
const VOLUME_STEP = 0.05

type PlayerKeysTarget = {
  state: {
    currentTime: number
    duration: number
    volume: number
  }
  seekRatio: (ratio: number) => void
  setVolume: (value: number) => void
}

/** 处理方向键，返回 true 表示已消费（调用方应停止后续快捷键判断）。 */
export function handlePlayerArrowKeys(event: KeyboardEvent, player: PlayerKeysTarget): boolean {
  const key = event.key
  if (key !== 'ArrowLeft' && key !== 'ArrowRight' && key !== 'ArrowUp' && key !== 'ArrowDown') {
    return false
  }
  const { currentTime, duration, volume } = player.state
  if (!duration) return false

  if (key === 'ArrowLeft' || key === 'ArrowRight') {
    const delta = key === 'ArrowRight' ? SEEK_STEP_SECONDS : -SEEK_STEP_SECONDS
    // seekRatio 内部会 clamp 到 0..1，这里不必自己夹一遍。
    player.seekRatio((currentTime + delta) / duration)
  } else {
    const delta = key === 'ArrowUp' ? VOLUME_STEP : -VOLUME_STEP
    // 四舍五入到百分比整数，避免 0.05 累加出 0.15000000000000002 这类浮点尾数。
    const next = Math.min(1, Math.max(0, Math.round((volume + delta) * 100) / 100))
    player.setVolume(next)
  }
  event.preventDefault()
  return true
}
