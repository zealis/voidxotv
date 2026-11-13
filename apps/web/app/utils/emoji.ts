// 评论表情：Google Noto 3D 表情（字体 SIL OFL 1.1），资源在 public/emoji3d，
// 由 scripts/sync-emoji3d.mjs 从参考资料里的字体导出，清单在 app/data/emoji.json。
//
// 为什么自托管：前端 CSP 的 img-src 只有 'self' data: blob: 与自有媒体源，
// 直接引第三方 CDN 会被拦；自托管也不受第三方可用性影响。

import manifest from '~/data/emoji.json'

export interface EmojiCategory {
  key: string
  label: string
  items: string[]
}

export const emojiCategories: EmojiCategory[] = manifest.categories

// 文件名是 code point 十六进制序列，变体选择符 FE0F 在文件名里省略。
// 归一化比较还能让「❤」与「❤️」命中同一张图。
const VARIATION_SELECTOR = /\uFE0F/g

function normalize(value: string): string {
  return value.replace(VARIATION_SELECTOR, '')
}

function fileName(char: string): string {
  const points = [...normalize(char)].map((part) => part.codePointAt(0)!.toString(16))
  return `${points.join('-')}.png`
}

const known = new Map<string, string>()
for (const category of emojiCategories) {
  for (const item of category.items) known.set(normalize(item), fileName(item))
}

/** 清单内表情返回同源 PNG 地址；清单外返回 null，调用方回落原字符。 */
export function emojiSrc(char: string): string | null {
  const name = known.get(normalize(char))
  return name ? `/emoji3d/${name}` : null
}

export interface EmojiPart {
  type: 'text' | 'emoji'
  value: string
}

// 基础字符 + 变体选择符 + 肤色修饰 + ZWJ 组合（👨‍👩‍👧、❤️‍🔥 这类要整体当一个表情）。
const EMOJI_SEQUENCE =
  /\p{Extended_Pictographic}(?:\uFE0F|[\u{1F3FB}-\u{1F3FF}])*(?:\u200D\p{Extended_Pictographic}(?:\uFE0F|[\u{1F3FB}-\u{1F3FF}])*)*/gu

/** 把一段文本切成「普通文字 / 表情」，供模板分别渲染。 */
export function splitEmoji(text: string): EmojiPart[] {
  const parts: EmojiPart[] = []
  let cursor = 0
  for (const match of text.matchAll(EMOJI_SEQUENCE)) {
    const index = match.index ?? 0
    if (index > cursor) parts.push({ type: 'text', value: text.slice(cursor, index) })
    parts.push({ type: 'emoji', value: match[0] })
    cursor = index + match[0].length
  }
  if (cursor < text.length) parts.push({ type: 'text', value: text.slice(cursor) })
  return parts
}
