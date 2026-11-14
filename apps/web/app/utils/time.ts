// 发布时间的人类可读写法（沉浸界面作者名右侧）：
//   1 分钟内      → 刚刚
//   1 小时内      → N分钟前
//   24 小时内     → N小时前
//   7 天内        → N天前
//   超过 7 天     → M月D日
//   超过 365 天   → YYYY年M月D日
// 规则来自需求：「超过 7 天才显示具体日期，超过一年才增加年份」。
const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
const WEEK = 7 * DAY
const YEAR = 365 * DAY

export function publishedLabel(value?: string | null, now: number = Date.now()): string {
  if (!value) return ''
  const time = new Date(value).getTime()
  if (Number.isNaN(time)) return ''
  const diff = now - time
  // 未来时间只可能是时钟误差或定时发布，别显示「-3分钟前」。
  if (diff < MINUTE) return '刚刚'
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)}分钟前`
  if (diff < DAY) return `${Math.floor(diff / HOUR)}小时前`
  if (diff < WEEK) return `${Math.floor(diff / DAY)}天前`

  const date = new Date(time)
  const monthDay = `${date.getMonth() + 1}月${date.getDate()}日`
  if (diff < YEAR) return monthDay
  return `${date.getFullYear()}年${monthDay}`
}
