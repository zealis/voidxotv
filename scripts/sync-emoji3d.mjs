// 把「评论表情」用到的 Noto 3D 表情从参考资料里的字体中导出成 PNG。
//
// 为什么不是复制图片：参考资料给的是 `Noto-3D-128*.ttf` 字体文件，图形全部以
// **sbix**（Apple 位图字体表）内嵌 PNG 的形式存在，没有散图。这里直接解析
// TTF 表结构（cmap 码点 → 字形号，sbix 字形 → 内嵌 PNG），零依赖导出。
//
// 为什么不整套搬：字体里有 1400+ 个表情（含大量冷门符号），只导出清单里的常用表情，
// 面板体积可控。清单唯一事实来源仍是 apps/web/app/data/emoji.json。
//
// 用法：
//   node scripts/sync-emoji3d.mjs              # 导出缺失/过期的 PNG
//   node scripts/sync-emoji3d.mjs --check      # 只校验，不写文件（CI 可用）
//   node scripts/sync-emoji3d.mjs --ppem=82    # 换一档位图尺寸（默认 55）
//
// 坑：这套字体没有 ZWJ 组合（😮‍💨、❤️‍🔥 这类）的字形，GSUB 里也没有对应连字，
// 所以清单里不放组合表情——它们会回落到系统字体，不会被渲染成错的图。

import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const manifestPath = join(root, 'apps/web/app/data/emoji.json')
const fontPath = join(root, '参考资料/Noto-3D-Emoji-Fonts/Noto-3D-128-noflags.ttf')
const licensePath = join(root, '参考资料/Noto-3D-Emoji-Fonts/LICENSE')
const targetDir = join(root, 'apps/web/public/emoji3d')

const args = process.argv.slice(2)
const checkOnly = args.includes('--check')
const wantPpem = Number(args.find((arg) => arg.startsWith('--ppem='))?.split('=')[1] || 55)

/** TTF 表目录：tag → 偏移与长度。 */
function readTables(buf) {
  const numTables = buf.readUInt16BE(4)
  const tables = new Map()
  for (let i = 0; i < numTables; i += 1) {
    const at = 12 + i * 16
    tables.set(buf.toString('latin1', at, at + 4), {
      offset: buf.readUInt32BE(at + 8),
      length: buf.readUInt32BE(at + 12),
    })
  }
  return tables
}

/** cmap：码点 → 字形号。格式 4（BMP）与格式 12（全平面，后写覆盖）。 */
function readCmap(buf, offset) {
  const subtables = []
  const count = buf.readUInt16BE(offset + 2)
  for (let i = 0; i < count; i += 1) {
    const record = offset + 4 + i * 8
    subtables.push(offset + buf.readUInt32BE(record + 4))
  }
  const map = new Map()
  // 先铺格式 4，再让格式 12 覆盖：按格式升序处理即可。
  subtables.sort((a, b) => buf.readUInt16BE(a) - buf.readUInt16BE(b))
  for (const sub of subtables) {
    const format = buf.readUInt16BE(sub)
    if (format === 4) {
      const segments = buf.readUInt16BE(sub + 6) / 2
      const endAt = sub + 14
      const startAt = endAt + segments * 2 + 2
      const deltaAt = startAt + segments * 2
      const rangeAt = deltaAt + segments * 2
      for (let i = 0; i < segments; i += 1) {
        const end = buf.readUInt16BE(endAt + i * 2)
        const start = buf.readUInt16BE(startAt + i * 2)
        const delta = buf.readInt16BE(deltaAt + i * 2)
        const rangeOffset = buf.readUInt16BE(rangeAt + i * 2)
        for (let cp = start; cp <= end && cp !== 0xffff; cp += 1) {
          let gid
          if (rangeOffset === 0) {
            gid = (cp + delta) & 0xffff
          } else {
            const at = rangeAt + i * 2 + rangeOffset + (cp - start) * 2
            gid = buf.readUInt16BE(at)
            if (gid !== 0) gid = (gid + delta) & 0xffff
          }
          if (gid !== 0) map.set(cp, gid)
        }
      }
      continue
    }
    if (format === 12) {
      const groups = buf.readUInt32BE(sub + 12)
      for (let i = 0; i < groups; i += 1) {
        const at = sub + 16 + i * 12
        const start = buf.readUInt32BE(at)
        const end = buf.readUInt32BE(at + 4)
        const startGlyph = buf.readUInt32BE(at + 8)
        for (let cp = start; cp <= end; cp += 1) map.set(cp, startGlyph + (cp - start))
      }
    }
  }
  return map
}

/** sbix：按 ppem 列出各 strike 的字形偏移表。 */
function readSbix(buf, offset, numGlyphs) {
  const strikes = []
  const count = buf.readUInt32BE(offset + 4)
  for (let i = 0; i < count; i += 1) {
    const start = offset + buf.readUInt32BE(offset + 8 + i * 4)
    const ppem = buf.readUInt16BE(start)
    const offsets = new Uint32Array(numGlyphs + 1)
    for (let g = 0; g <= numGlyphs; g += 1) offsets[g] = buf.readUInt32BE(start + 4 + g * 4)
    strikes.push({ ppem, start, offsets })
  }
  return strikes
}

/** 取一个字形的内嵌 PNG；该 strike 没有位图时返回 null。 */
function glyphPng(buf, strike, gid) {
  const from = strike.start + strike.offsets[gid]
  const to = strike.start + strike.offsets[gid + 1]
  if (to - from <= 8) return null
  const type = buf.toString('latin1', from + 4, from + 8)
  return type === 'png ' ? buf.subarray(from + 8, to) : null
}

/** 文件名与 Twemoji 时期保持一致：码点十六进制、去掉变体选择符 FE0F。 */
function fileName(char) {
  const points = [...char]
    .map((part) => part.codePointAt(0))
    .filter((cp) => cp !== 0xfe0f)
    .map((cp) => cp.toString(16))
  return `${points.join('-')}.png`
}

if (!existsSync(fontPath)) {
  console.error(`找不到表情字体：${fontPath}`)
  console.error('请把 Noto-3D-Emoji-Fonts 放到 参考资料/ 下（该目录不入库）。')
  process.exit(1)
}

const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
const font = readFileSync(fontPath)
const tables = readTables(font)
const cmap = readCmap(font, tables.get('cmap').offset)
const numGlyphs = font.readUInt16BE(tables.get('maxp').offset + 4)
const strikes = readSbix(font, tables.get('sbix').offset, numGlyphs)
// 选「不小于目标尺寸」的最小一档：既够清晰，又不至于把 100+KB 的大图搬进来。
const usable = strikes.filter((strike) => strike.ppem >= wantPpem).sort((a, b) => a.ppem - b.ppem)
const strike = usable[0] || strikes.sort((a, b) => b.ppem - a.ppem)[0]
if (!strike) {
  console.error('字体里没有 sbix 位图，无法导出。')
  process.exit(1)
}

if (!checkOnly && !existsSync(targetDir)) mkdirSync(targetDir, { recursive: true })

const missing = []
const stale = []
const wanted = new Set()
let written = 0
let kept = 0
let bytes = 0

for (const category of manifest.categories) {
  for (const char of category.items) {
    const name = fileName(char)
    wanted.add(name)
    // 变体选择符 FE0F 只是「强制彩色呈现」，字形还是基础码点那个；查字形前要先去掉。
    const codepoints = [...char].map((part) => part.codePointAt(0)).filter((cp) => cp !== 0xfe0f)
    const gid = codepoints.length === 1 ? cmap.get(codepoints[0]) : undefined
    const png = gid === undefined ? null : glyphPng(font, strike, gid)
    if (!png) {
      missing.push({ char, category: category.key, reason: codepoints.length > 1 ? '组合表情（字体无此字形）' : '字体里没有该码点' })
      continue
    }
    const target = join(targetDir, name)
    if (existsSync(target) && statSync(target).size === png.length) {
      kept += 1
      bytes += png.length
      continue
    }
    if (!checkOnly) writeFileSync(target, png)
    written += 1
    bytes += png.length
  }
}

if (!checkOnly) {
  for (const name of readdirSync(targetDir)) {
    if (name.endsWith('.png') && !wanted.has(name)) stale.push(name)
  }
}

const total = manifest.categories.reduce((sum, category) => sum + category.items.length, 0)
console.log(
  `表情清单 ${total} 个：新导出 ${written}，已存在 ${kept}，缺失 ${missing.length}`
  + `（strike ${strike.ppem}px，合计 ${(bytes / 1024).toFixed(0)}KB）`,
)
if (stale.length) {
  console.log(`  静态目录有 ${stale.length} 个清单外文件（可手动清理）：${stale.slice(0, 6).join(', ')}${stale.length > 6 ? '…' : ''}`)
}
if (missing.length) {
  for (const item of missing.slice(0, 20)) console.log(`  缺失 ${item.char}（${item.category}）：${item.reason}`)
  if (missing.length > 20) console.log(`  …还有 ${missing.length - 20} 个，见清单`)
  process.exit(checkOnly ? 1 : 2)
}

// 字体是 SIL OFL 1.1：随图形资源一起分发许可原文。
const licenseTarget = join(targetDir, 'LICENSE')
if (existsSync(licensePath) && !existsSync(licenseTarget) && !checkOnly) {
  copyFileSync(licensePath, licenseTarget)
}

if (!checkOnly) console.log(`静态目录：apps/web/public/emoji3d/（${wanted.size} 个 PNG）`)
