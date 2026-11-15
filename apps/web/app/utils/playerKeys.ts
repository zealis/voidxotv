// 播放器方向键快捷键：左右 ±5 秒定位，上下 ±5% 音量。
// 首页沉浸流与视频详情页共用这一份实现，避免两处行为漂移。
// 只在拿到视频时长时生效；图片 / 还未加载出时长时忽略，避免抢走页面滚动。
// 首页沉浸流要把 ↑/↓ 留给「切换上一条 / 下一条视频」，调用时传
// `{ volumeKeys: false }`；详情页只有一条视频，上下键才用来调音量。

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

/** 本次按键动作：null 表示未消费，调用方应继续其它快捷键判断。 */
export type PlayerKeyAction = 'seek' | 'volume' | null

type PlayerKeysOptions = {
  /** 是否用 ↑/↓ 调音量；首页沉浸流要留着上下键切视频，传 false。 */
  volumeKeys?: boolean
}

/**
 * 处理方向键并回报动作。区分 seek / volume 是为了让页面在键盘改音量时通知控制条
 * 把音量指示条唤出来——只改 player.state.volume 组件是看不见的。
 */
export function handlePlayerArrowKeys(
  event: KeyboardEvent,
  player: PlayerKeysTarget,
  options: PlayerKeysOptions = {},
): PlayerKeyAction {
  const key = event.key
  if (key !== 'ArrowLeft' && key !== 'ArrowRight' && key !== 'ArrowUp' && key !== 'ArrowDown') {
    return null
  }
  if ((key === 'ArrowUp' || key === 'ArrowDown') && options.volumeKeys === false) return null
  const { currentTime, duration, volume } = player.state
  if (!duration) return null

  if (key === 'ArrowLeft' || key === 'ArrowRight') {
    const delta = key === 'ArrowRight' ? SEEK_STEP_SECONDS : -SEEK_STEP_SECONDS
    // seekRatio 内部会 clamp 到 0..1，这里不必自己夹一遍。
    player.seekRatio((currentTime + delta) / duration)
    event.preventDefault()
    return 'seek'
  }

  const delta = key === 'ArrowUp' ? VOLUME_STEP : -VOLUME_STEP
  // 四舍五入到百分比整数，避免 0.05 累加出 0.15000000000000002 这类浮点尾数。
  const next = Math.min(1, Math.max(0, Math.round((volume + delta) * 100) / 100))
  player.setVolume(next)
  event.preventDefault()
  return 'volume'
}
