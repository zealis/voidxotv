#!/usr/bin/env node
// 夜街容量估算（开发技术规范 §5.2「每小时 GB ≈ 总 Mbps × 0.45」）。
// 用法：
//   node scripts/capacity.mjs
//   node scripts/capacity.mjs --per-day=2000 --duration=45 --hevc --json
//   node scripts/capacity.mjs --plays=500000 --cdn-price=0.18 --storage-price=0.015
// 说明：这是容量规划口径，不是账单；实际以代表性内容测量为准。

const args = new Map()
for (const raw of process.argv.slice(2)) {
  const [key, value = 'true'] = raw.replace(/^--/, '').split('=')
  args.set(key, value)
}
const number = (key, fallback) => {
  const parsed = Number(args.get(key))
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback
}
const flag = (key) => args.get(key) === 'true'

const perDay = number('per-day', 1000)
const durationSeconds = number('duration', 60)
const playsPerContent = number('plays', 2000)
const cdnPrice = number('cdn-price', 0.2) // 元/GB（示例价，按实际合同调整）
const storagePrice = number('storage-price', 0.02) // 元/GB/月
const hevc = flag('hevc')

// 码率基线来自 §5.2；音频按 128 kbps，mezzanine 按 192 kbps。
const LADDERS = {
  h264: [
    { name: '480p HLS', videoMbps: 1.2 },
    { name: '720p HLS', videoMbps: 2.8 },
  ],
  h264Short: [{ name: '720p MP4', videoMbps: 2.8 }],
  hevc: [{ name: '720p HEVC', videoMbps: 1.8 }],
  mezzanine: [{ name: '1080p mezzanine', videoMbps: 10, audioKbps: 192 }],
  aux: [
    { name: '封面 + poster', megabytes: 0.35 },
    { name: 'storyboard + preview', megabytes: 0.6 },
  ],
}

const AUDIO_MBPS = 0.128
const GB_PER_HOUR_PER_MBPS = 0.45

function ladderRows() {
  const rows = []
  const audio = AUDIO_MBPS
  const push = (entry, audioKbps = 128) => {
    const totalMbps = entry.videoMbps + audioKbps / 1000
    const gbPerHour = totalMbps * GB_PER_HOUR_PER_MBPS
    const mbPerItem = (totalMbps * durationSeconds) / 8
    rows.push({
      name: entry.name,
      totalMbps: Number(totalMbps.toFixed(3)),
      gbPerHour: Number(gbPerHour.toFixed(3)),
      mbPerItem: Number(mbPerItem.toFixed(2)),
    })
  }
  for (const entry of LADDERS.h264) push(entry)
  if (hevc) for (const entry of LADDERS.hevc) push(entry)
  for (const entry of LADDERS.mezzanine) push(entry, entry.audioKbps || 192)
  return rows
}

function main() {
  const rows = ladderRows()
  const primary = rows.filter((row) => !row.name.includes('mezzanine'))
  const mezzanine = rows.find((row) => row.name.includes('mezzanine'))

  const primaryMB = primary.reduce((sum, row) => sum + row.mbPerItem, 0)
  const auxMB = LADDERS.aux.reduce((sum, entry) => sum + entry.megabytes, 0)
  const mezzanineMB = mezzanine ? mezzanine.mbPerItem : 0

  const perItemGB = (primaryMB + auxMB + mezzanineMB) / 1024
  const dailyGB = perItemGB * perDay
  const monthlyGB = dailyGB * 30
  // 原件保留 7 天、mezzanine 保留 30 天（§5.4 / §7.4）。
  const hotStorageGB = (dailyGB * 7) + ((mezzanineMB / 1024) * perDay * 30)
  // 缓存命中 35% ⇒ 回源/出网比例为 65%。
  const CDN_MISS_RATIO = 0.65
  const cdnMonthlyGB = perItemGB * perDay * 30 * playsPerContent * CDN_MISS_RATIO

  const report = {
    assumptions: {
      perDay, durationSeconds, playsPerContent, hevc,
      cdnPricePerGB: cdnPrice, storagePricePerGBMonth: storagePrice,
      note: '码率来自 §5.2；缓存命中率按 35% 估算，需以真实 CDN 日志校准',
    },
    ladder: rows,
    perItem: {
      primaryMB: Number(primaryMB.toFixed(2)),
      auxiliaryMB: Number(auxMB.toFixed(2)),
      mezzanineMB: Number(mezzanineMB.toFixed(2)),
      totalGB: Number(perItemGB.toFixed(3)),
    },
    daily: {
      newContentGB: Number(dailyGB.toFixed(1)),
      hotStorageGB: Number(hotStorageGB.toFixed(1)),
      monthlyNewGB: Number(monthlyGB.toFixed(1)),
      monthlyStorageCostCNY: Number((hotStorageGB * storagePrice).toFixed(2)),
      monthlyCDNGB: Number(cdnMonthlyGB.toFixed(0)),
      monthlyCDNCostCNY: Number((cdnMonthlyGB * cdnPrice).toFixed(2)),
    },
  }

  if (flag('json')) {
    console.log(JSON.stringify(report, null, 2))
    return
  }

  const pad = (value, width) => String(value).padStart(width)
  console.log('夜街容量估算（口径：§5.2，每小时 GB ≈ 总 Mbps × 0.45）')
  console.log(`假设：每日新增 ${perDay} 条 · 平均 ${durationSeconds}s · 单条播放 ${playsPerContent} 次 · HEVC ${hevc ? '开启' : '关闭'}`)
  console.log('')
  console.log('档位                总Mbps   GB/小时   单条MB')
  for (const row of rows) {
    console.log(
      row.name.padEnd(18) + pad(row.totalMbps, 8) + pad(row.gbPerHour, 10) + pad(row.mbPerItem, 9),
    )
  }
  console.log('')
  console.log(`单条内容：主档 ${primaryMB.toFixed(1)} MB + 辅助 ${auxMB.toFixed(1)} MB + mezzanine ${mezzanineMB.toFixed(1)} MB = ${perItemGB.toFixed(3)} GB`)
  console.log(`每日新增存储：${dailyGB.toFixed(1)} GB；月度：${monthlyGB.toFixed(1)} GB`)
  console.log(`热存储（原件 7 天 + mezzanine 30 天）：${hotStorageGB.toFixed(1)} GB ≈ ￥${report.daily.monthlyStorageCostCNY}/月`)
  console.log(`CDN 出网（月，35% 命中率）：${cdnMonthlyGB.toFixed(0)} GB ≈ ￥${report.daily.monthlyCDNCostCNY}/月`)
  console.log('')
  console.log('提示：--json 输出机器可读结果；单价与命中率需按真实合同/日志替换。')
}

main()
