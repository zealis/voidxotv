// 播放器左右键：±5 秒定位。首页沉浸流与视频详情页共用这一份实现。
//
// 上下键不在这里处理：首页沉浸流用它切换上一条 / 下一条视频，且必须和滚轮 /
// 触摸滑动走同一套 scroll-snap + IntersectionObserver 路径（见 pages/index.vue
// 的 go() 与 setupObserver()）；详情页只有一条视频，没有可切换的下一条。
// 音量只由控制条滑块 / 静音键控制，不再占用方向键。
// 只在拿到视频时长时生效；图片 / 还未加载出时长时忽略，避免抢走页面滚动。

const SEEK_STEP_SECONDS = 5

type PlayerKeysTarget = {
  state: {
    currentTime: number
    duration: number
  }
  seekRatio: (ratio: number) => void
}

/** 处理左右键，返回 true 表示已消费；上下键交给页面。 */
export function handlePlayerArrowKeys(event: KeyboardEvent, player: PlayerKeysTarget): boolean {
  const key = event.key
  if (key !== 'ArrowLeft' && key !== 'ArrowRight') return false
  const { currentTime, duration } = player.state
  if (!duration) return false

  const delta = key === 'ArrowRight' ? SEEK_STEP_SECONDS : -SEEK_STEP_SECONDS
  // seekRatio 内部会 clamp 到 0..1，这里不必自己夹一遍。
  player.seekRatio((currentTime + delta) / duration)
  event.preventDefault()
  return true
}
