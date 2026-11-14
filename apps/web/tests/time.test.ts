// 发布时间文案的边界：7 天内相对、超过 7 天出日期、超过一年才带年份。
import { describe, expect, it } from 'vitest'
import { publishedLabel } from '../app/utils/time'

// 固定「现在」为 2026-11-15 12:00（本地时区），避免测试随运行时间漂移。
const NOW = new Date('2026-11-15T12:00:00+08:00').getTime()
const at = (iso: string) => publishedLabel(iso, NOW)

describe('publishedLabel', () => {
  it('空值 / 非法值返回空串，调用方据此不渲染', () => {
    expect(publishedLabel(undefined, NOW)).toBe('')
    expect(publishedLabel('', NOW)).toBe('')
    expect(publishedLabel('not-a-date', NOW)).toBe('')
  })

  it('一分钟内是「刚刚」，未来时间也按「刚刚」处理', () => {
    expect(at('2026-11-15T11:59:30+08:00')).toBe('刚刚')
    expect(at('2026-11-15T12:05:00+08:00')).toBe('刚刚')
  })

  it('一小时内按分钟', () => {
    expect(at('2026-11-15T11:31:00+08:00')).toBe('29分钟前')
    expect(at('2026-11-15T11:00:01+08:00')).toBe('59分钟前')
  })

  it('24 小时内按小时', () => {
    expect(at('2026-11-15T09:00:00+08:00')).toBe('3小时前')
    expect(at('2026-11-14T12:30:00+08:00')).toBe('23小时前')
  })

  it('1–6 天按天', () => {
    expect(at('2026-11-14T11:00:00+08:00')).toBe('1天前')
    expect(at('2026-11-11T00:21:00+08:00')).toBe('4天前')
    expect(at('2026-11-09T11:00:00+08:00')).toBe('6天前')
  })

  it('满 7 天改显示日期，且不带年份', () => {
    expect(at('2026-11-08T11:59:00+08:00')).toBe('11月8日')
    expect(at('2026-03-02T08:00:00+08:00')).toBe('3月2日')
  })

  it('超过一年才带年份', () => {
    // 364 天前：仍不带年
    expect(at('2025-11-16T12:00:00+08:00')).toBe('11月16日')
    // 366 天前：带年
    expect(at('2025-11-14T12:00:00+08:00')).toBe('2025年11月14日')
    expect(at('2023-01-05T12:00:00+08:00')).toBe('2023年1月5日')
  })
})
