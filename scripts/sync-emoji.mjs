// 从参考资料里的 Twemoji 14.0.2 同步「评论表情面板」用到的 SVG 到前端静态目录。
//
// 为什么要复制而不是直接引 CDN：前端 CSP 是 `img-src 'self' data: blob:`，
// 外链图片会被拦；复制到 public/emoji 后与站点同源，也不依赖第三方可用性。
//
// 用法：
//   node scripts/sync-emoji.mjs            # 复制缺失/更新的资源
//   node scripts/sync-emoji.mjs --check    # 只校验，不写文件（CI 可用）
//
// 清单唯一事实来源是 apps/web/app/data/emoji.json：加表情只需往分类里加字符，
// 然后重跑本脚本。

import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const manifestPath = join(root, 'apps/web/app/data/emoji.json')
const sourceDir = join(root, '参考资料/twemoji-14.0.2/assets/svg')
const targetDir = join(root, 'apps/web/public/emoji')
const checkOnly = process.argv.includes('--check')

const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))

// Twemoji 文件名是 code point 的十六进制序列（如 1f468-200d-1f469-200d-1f467），
// 变体选择符 FE0F 在文件名里通常省略——所以候选名要覆盖「去 FE0F」与「保留」两种。
function candidates(char) {
  const points = [...char].map((part) => part.codePointAt(0).toString(16))
  const withoutVariation = points.filter((point) => point !== 'fe0f')
  const names = []
  if (withoutVariation.length) names.push(`${withoutVariation.join('-')}.svg`)
  if (points.length && points.join('-') !== withoutVariation.join('-')) {
    names.push(`${points.join('-')}.svg`)
  }
  return names
}

if (!existsSync(sourceDir)) {
  console.error(`找不到 Twemoji 资源目录：${sourceDir}`)
  console.error('请把 twemoji-14.0.2 解压到 参考资料/ 下（该目录不入库）。')
  process.exit(1)
}

if (!checkOnly && !existsSync(targetDir)) mkdirSync(targetDir, { recursive: true })

const missing = []
let copied = 0
let kept = 0

for (const category of manifest.categories) {
  for (const char of category.items) {
    const names = candidates(char)
    const source = names.map((name) => join(sourceDir, name)).find((path) => existsSync(path))
    if (!source) {
      missing.push({ char, category: category.key, names })
      continue
    }
    const target = join(targetDir, names.find((name) => existsSync(join(sourceDir, name))))
    if (existsSync(target)) {
      kept += 1
      continue
    }
    if (!checkOnly) copyFileSync(source, target)
    copied += 1
  }
}

// 图形资源是 CC-BY 4.0，必须随分发提供署名。
const licenseSource = join(root, '参考资料/twemoji-14.0.2/LICENSE-GRAPHICS')
const licenseTarget = join(targetDir, 'LICENSE-GRAPHICS')
if (existsSync(licenseSource) && !existsSync(licenseTarget) && !checkOnly) {
  copyFileSync(licenseSource, licenseTarget)
}

const total = manifest.categories.reduce((sum, category) => sum + category.items.length, 0)
console.log(`表情清单 ${total} 个：新复制 ${copied}，已存在 ${kept}，缺失 ${missing.length}`)
if (missing.length) {
  for (const item of missing.slice(0, 20)) {
    console.log(`  缺失 ${item.char}（${item.category}）候选文件名：${item.names.join(' / ')}`)
  }
  if (missing.length > 20) console.log(`  …还有 ${missing.length - 20} 个，见清单`)
  process.exit(checkOnly ? 1 : 2)
}

if (!checkOnly) {
  const files = readdirSync(targetDir).filter((name) => name.endsWith('.svg'))
  console.log(`静态目录现有 ${files.length} 个 SVG：apps/web/public/emoji/`)
}
