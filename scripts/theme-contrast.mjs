// 校验工具型页面深浅主题的 WCAG 2.2 AA 对比度（UI 规范 §14 / §15）。
// 直接解析 tokens.css 里的 [data-surface="tool"] 变量，避免脚本内重复定义色值。
// 普通文字 >= 4.5:1；控件边界 / 大字 >= 3:1。

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const css = readFileSync(join(root, 'apps/web/app/assets/css/tokens.css'), 'utf8')

// 深色默认值挂在 :root（供跨作用域共享控件继承），浅色值挂在
// html[data-tool-theme="light"] [data-surface="tool"]。按 --vp-tool-bg
// 的出现次序定位块，避免依赖易变的选择器文本。
function block(index) {
  const positions = [...css.matchAll(/--vp-tool-bg:/g)].map((m) => m.index)
  if (positions.length < 2) throw new Error('未找到深浅两套 --vp-tool-bg 定义')
  const anchor = positions[index]
  const open = css.lastIndexOf('{', anchor)
  const close = css.indexOf('\n}', anchor)
  if (open < 0 || close < 0) throw new Error(`第 ${index + 1} 套色板缺少块边界`)
  const body = css.slice(open + 1, close)
  const vars = {}
  for (const [, name, value] of body.matchAll(/(--vp-tool-[\w-]+):\s*([^;]+);/g)) {
    vars[name] = value.trim()
  }
  return vars
}

function parse(color) {
  const hex = color.match(/^#([0-9a-f]{6})$/i)
  if (hex) {
    const n = parseInt(hex[1], 16)
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255, 1]
  }
  const rgba = color.match(/^rgba?\(([^)]+)\)$/i)
  if (rgba) {
    const parts = rgba[1].split(',').map((s) => s.trim())
    return [Number(parts[0]), Number(parts[1]), Number(parts[2]), parts[3] === undefined ? 1 : Number(parts[3])]
  }
  throw new Error(`无法解析颜色：${color}`)
}

// 把带 alpha 的前景色合成到不透明背景上（深色卡片是半透明白）。
function flatten(fg, bg) {
  const [r, g, b, a] = parse(fg)
  const [br, bgc, bb] = parse(bg)
  return [r * a + br * (1 - a), g * a + bgc * (1 - a), b * a + bb * (1 - a), 1]
}

function luminance([r, g, b]) {
  const linear = [r, g, b].map((v) => {
    const s = v / 255
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]
}

function contrast(a, b) {
  const la = luminance(a)
  const lb = luminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

// [名称, 前景变量, 背景变量, 最低比值, 说明]
const checks = [
  ['主文字 / 页面底', '--vp-tool-text', '--vp-tool-bg', 4.5],
  ['主文字 / 卡片', '--vp-tool-text', '--vp-tool-surface', 4.5],
  ['次文字 / 页面底', '--vp-tool-text-2', '--vp-tool-bg', 4.5],
  ['次文字 / 卡片', '--vp-tool-text-2', '--vp-tool-surface', 4.5],
  ['强调文字 / 页面底', '--vp-tool-accent-ink', '--vp-tool-bg', 4.5],
  ['强调文字 / 卡片', '--vp-tool-accent-ink', '--vp-tool-surface', 4.5],
  ['主按钮文字 / 按钮底', '--vp-tool-on-accent', '--vp-tool-accent', 4.5],
  ['危险文字 / 提示底', '--vp-tool-danger-ink', '--vp-tool-danger-soft', 4.5],
  ['正面文字 / 卡片', '--vp-tool-positive', '--vp-tool-surface', 4.5],
  ['中性徽标 / 卡片', '--vp-tool-neutral', '--vp-tool-surface', 4.5],
  ['输入框文字 / 输入框底', '--vp-tool-text', '--vp-tool-input', 4.5],
  ['控件边界 / 卡片', '--vp-tool-border-strong', '--vp-tool-surface', 3],
]

let failed = 0
for (const [index, theme, selector] of [
  [0, '深色', ':root（默认）'],
  [1, '浅色', 'html[data-tool-theme="light"] [data-surface="tool"]'],
]) {
  const vars = block(index)
  console.log(`\n=== ${theme}（${selector}）===`)
  for (const [name, fgKey, bgKey, min] of checks) {
    const fg = vars[fgKey]
    const bg = vars[bgKey]
    if (!fg || !bg) {
      console.log(`  SKIP ${name}：缺少变量 ${!fg ? fgKey : bgKey}`)
      failed++
      continue
    }
    const base = parse(vars['--vp-tool-bg'])
    // 背景可能半透明：先合成到页面底，再合成前景。
    const bgSolid = flatten(bg, `rgb(${base[0]},${base[1]},${base[2]})`)
    const fgSolid = flatten(fg, `rgb(${bgSolid[0]},${bgSolid[1]},${bgSolid[2]})`)
    const ratio = contrast(fgSolid, bgSolid)
    const ok = ratio >= min
    if (!ok) failed++
    console.log(`  ${ok ? 'PASS' : 'FAIL'} ${name.padEnd(22)} ${ratio.toFixed(2)}:1 (需 ≥ ${min})`)
  }
}

console.log(`\n${failed === 0 ? '全部通过' : `${failed} 项未达标`}`)
process.exit(failed === 0 ? 0 : 1)
