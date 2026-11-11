// 版本号提升：把 VERSION / 两个 package.json / Go 构建常量改成同一个值，
// 并把 CHANGELOG.md 里的 [未发布] 段落转成正式版本条目。
//
// 用法：
//   node scripts/bump-version.mjs patch        # 0.5.1 -> 0.5.2（只有修复与微调）
//   node scripts/bump-version.mjs minor        # 0.5.1 -> 0.6.0（新增功能）
//   node scripts/bump-version.mjs major        # 0.5.1 -> 1.0.0（对外接口不兼容）
//   node scripts/bump-version.mjs 0.7.0        # 显式指定
//   node scripts/bump-version.mjs minor --dry-run
//   node scripts/bump-version.mjs --check      # 只校验四处版本号是否一致（CI 用）
//
// 规则见 CHANGELOG.md 顶部「版本规则」；本脚本只改版本号，条目内容仍需人写。

import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const dryRun = args.includes('--dry-run')
const checkOnly = args.includes('--check')
const target = args.find((arg) => !arg.startsWith('--'))

const semver = /^\d+\.\d+\.\d+$/
const current = readFileSync(join(root, 'VERSION'), 'utf8').trim()

if (checkOnly) {
  const sources = [
    ['VERSION', current],
    ['package.json', /"version"\s*:\s*"([^"]*)"/.exec(readFileSync(join(root, 'package.json'), 'utf8'))?.[1]],
    ['apps/web/package.json', /"version"\s*:\s*"([^"]*)"/.exec(readFileSync(join(root, 'apps/web/package.json'), 'utf8'))?.[1]],
    [
      'internal/platform/buildinfo/buildinfo.go',
      /var Version = "([^"]*)"/.exec(readFileSync(join(root, 'internal/platform/buildinfo/buildinfo.go'), 'utf8'))?.[1],
    ],
  ]
  const drift = sources.filter(([, value]) => value !== current)
  if (drift.length) {
    console.error(`版本号不一致（基准 VERSION = ${current}）：`)
    for (const [name, value] of drift) console.error(`  - ${name}: ${value ?? '(缺失)'}`)
    console.error('修正：node scripts/bump-version.mjs <当前版本>')
    process.exit(1)
  }
  console.log(`版本号一致：${current}（VERSION / 2 个 package.json / buildinfo.go）`)
  process.exit(0)
}

if (!target) {
  console.error('用法：node scripts/bump-version.mjs <patch|minor|major|X.Y.Z> [--dry-run] | --check')
  process.exit(1)
}

if (!semver.test(current)) {
  console.error(`VERSION 内容不是合法版本号：${current}`)
  process.exit(1)
}

function nextVersion(from, kind) {
  if (semver.test(kind)) return kind
  const [major, minor, patch] = from.split('.').map(Number)
  if (kind === 'major') return `${major + 1}.0.0`
  if (kind === 'minor') return `${major}.${minor + 1}.0`
  if (kind === 'patch') return `${major}.${minor}.${patch + 1}`
  console.error(`无法识别的参数：${kind}（只接受 patch / minor / major / X.Y.Z）`)
  process.exit(1)
}

const next = nextVersion(current, target)
const today = new Date().toISOString().slice(0, 10)
const writes = []

// 1) VERSION
writes.push([join(root, 'VERSION'), `${next}\n`])

// 2) 两个 package.json：只改 version 字段，保留原有键顺序与缩进
for (const rel of ['package.json', 'apps/web/package.json']) {
  const raw = readFileSync(join(root, rel), 'utf8')
  if (!/"version"\s*:/.test(raw)) {
    console.error(`${rel} 里没有 version 字段，先补上再运行本脚本`)
    process.exit(1)
  }
  writes.push([join(root, rel), raw.replace(/("version"\s*:\s*")[^"]*(")/, `$1${next}$2`)])
}

// 3) Go 构建常量（/health/live 与启动日志都读它）
const goRel = 'internal/platform/buildinfo/buildinfo.go'
const goRaw = readFileSync(join(root, goRel), 'utf8')
if (!/var Version = "[^"]*"/.test(goRaw)) {
  console.error('buildinfo.go 里找不到 var Version，先确认文件结构')
  process.exit(1)
}
writes.push([join(root, goRel), goRaw.replace(/(var Version = ")[^"]*(")/, `$1${next}$2`)])

// 4) CHANGELOG：把 [未发布] 的正文搬进新版本条目，再留一个空的 [未发布]
const changelogRaw = readFileSync(join(root, 'CHANGELOG.md'), 'utf8')
const unreleased = /^## \[未发布\]\s*\n([\s\S]*?)(?=^## \[)/m.exec(changelogRaw)
if (!unreleased) {
  console.error('CHANGELOG.md 里找不到 "## [未发布]" 段落，请先修正文件结构')
  process.exit(1)
}
// 占位注释不算条目，否则会被一起搬进正式版本条目里。
const body = unreleased[1]
  .split('\n')
  .filter((line) => !line.includes('下次改动写在这里'))
  .join('\n')
  .replace(/\n{3,}/g, '\n\n')
  .trim()
const hasEntries = /^### /m.test(body)
const released = [
  '## [未发布]',
  '',
  '<!-- 下次改动写在这里；发布时由 bump-version.mjs 提升为正式版本条目。 -->',
  '',
  `## [${next}] - ${today}`,
  '',
  body || '### 改进\n\n- （无条目）',
  '',
  '',
].join('\n')
writes.push([
  join(root, 'CHANGELOG.md'),
  changelogRaw.slice(0, unreleased.index) + released + changelogRaw.slice(unreleased.index + unreleased[0].length),
])

if (dryRun) {
  console.log(`[dry-run] 版本 ${current} -> ${next}，将改动：`)
  for (const [path] of writes) console.log(`  - ${path.replace(root, '.')}`)
  if (!hasEntries) console.log('  ! [未发布] 里没有 ### 条目，正式版本会写成「（无条目）」')
  process.exit(0)
}

for (const [path, content] of writes) writeFileSync(path, content, 'utf8')

console.log(`版本：${current} -> ${next}`)
console.log('已同步：VERSION / package.json / apps/web/package.json / buildinfo.go / CHANGELOG.md')
if (!hasEntries) {
  console.log('注意：[未发布] 里没有 ### 条目，建议先按分类写好条目再发布。')
}
console.log('下一步：gofmt -l ./cmd ./internal && go build ./... && cd apps/web && npx nuxi typecheck')
