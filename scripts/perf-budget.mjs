#!/usr/bin/env node
// 首屏资源预算检查（开发技术规范 §12.1：首屏 JavaScript ≤ 200 KB gzip）。
// 数据来源是构建产物，不需要运行中的服务：
//   node scripts/perf-budget.mjs
//   node scripts/perf-budget.mjs --json --budget=200
// 说明：脚本统计预渲染 HTML 实际引用的 JS/CSS；沉浸首页为 SSR，
// 其共享 chunk 与预渲染页一致，播放器与后台 chunk 按路由懒加载。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { join, relative, sep } from 'node:path'

const args = new Map()
for (const raw of process.argv.slice(2)) {
  const [key, value = 'true'] = raw.replace(/^--/, '').split('=')
  args.set(key, value)
}
const json = args.get('json') === 'true'
const budgetKB = Number(args.get('budget')) > 0 ? Number(args.get('budget')) : 200
const root = join(process.cwd(), 'apps', 'web', '.output', 'public')

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...walk(full))
    else out.push(full)
  }
  return out
}

let files
try {
  files = walk(root)
} catch {
  console.error(`找不到构建产物：${root}。请先运行 npm run build --prefix apps/web。`)
  process.exit(2)
}

const htmls = files.filter((file) => file.endsWith('.html'))
const refs = new Set()
for (const html of htmls) {
  const text = readFileSync(html, 'utf8')
  for (const match of text.matchAll(/(?:src|href)="(\/_nuxt\/[^"]+)"/g)) {
    refs.add(match[1].split('?')[0])
  }
}

const rows = []
for (const url of refs) {
  const path = join(root, url.replace(/^\//, '').split('/').join(sep))
  try {
    const buffer = readFileSync(path)
    rows.push({
      url,
      kind: url.endsWith('.css') ? 'css' : 'js',
      raw: buffer.length,
      gzip: gzipSync(buffer, { level: 9 }).length,
    })
  } catch {
    // 引用了但产物缺失：记录下来，不影响其余统计。
    rows.push({ url, kind: 'missing', raw: 0, gzip: 0 })
  }
}

const sum = (kind) => rows.filter((r) => r.kind === kind)
  .reduce((total, r) => total + r.gzip, 0)
const jsGzip = sum('js')
const cssGzip = sum('css')
const jsKB = jsGzip / 1024
const ok = jsKB <= budgetKB

// 全量 chunk 中体积最大的几个，用于判断代码分割是否合理。
const allJs = files.filter((file) => file.endsWith('.js'))
  .map((file) => {
    const buffer = readFileSync(file)
    return {
      file: relative(root, file).split(sep).join('/'),
      gzip: gzipSync(buffer, { level: 9 }).length,
      entry: refs.has('/' + relative(root, file).split(sep).join('/')),
    }
  })
  .sort((a, b) => b.gzip - a.gzip)
  .slice(0, 8)

const report = {
  budgetKB,
  htmlPages: htmls.length,
  entry: {
    jsGzipKB: Number(jsKB.toFixed(1)),
    cssGzipKB: Number((cssGzip / 1024).toFixed(1)),
    files: rows.sort((a, b) => b.gzip - a.gzip),
    withinBudget: ok,
  },
  largestChunks: allJs.map((chunk) => ({
    file: chunk.file,
    gzipKB: Number((chunk.gzip / 1024).toFixed(1)),
    referencedByEntry: chunk.entry,
  })),
  note: 'LCP/INP/CLS 需真实用户采集；本脚本只覆盖可由构建产物验证的体积预算。',
}

if (json) {
  console.log(JSON.stringify(report, null, 2))
  process.exit(ok ? 0 : 1)
}

console.log('夜街首屏资源预算（§12.1）')
console.log(`预渲染页面：${htmls.length} 个 · 预算：${budgetKB} KB gzip`)
console.log('')
console.log('入口引用：')
for (const row of report.entry.files) {
  console.log(`  ${row.kind.padEnd(7)} ${(row.gzip / 1024).toFixed(1).padStart(6)} KB gzip  ${row.url}`)
}
console.log('')
console.log(`首屏 JavaScript：${jsKB.toFixed(1)} KB gzip（${ok ? 'PASS' : 'FAIL'}，预算 ${budgetKB} KB）`)
console.log(`首屏 CSS：${(cssGzip / 1024).toFixed(1)} KB gzip`)
console.log('')
console.log('最大 chunk（判断是否按路由拆分）：')
for (const chunk of report.largestChunks) {
  console.log(`  ${String(chunk.gzipKB).padStart(6)} KB  ${chunk.referencedByEntry ? '[入口] ' : ''}${chunk.file}`)
}
console.log('')
console.log(report.note)
process.exit(ok ? 0 : 1)
