// 回归：错误提示面向用户，错误码 / HTTP 状态 / 英文内部错误只在代码与日志里出现。
// 对应 CHANGELOG 0.7.9 的「界面不展示开发术语」。
import { describe, expect, it } from 'vitest'
import { ApiError, errorMessage } from '../app/composables/useApi'

describe('errorMessage', () => {
  it('CSRF 失效只提示用户重新登录，不泄露错误码与术语', () => {
    const text = errorMessage(new ApiError(503, 'csrf_unavailable', '请重新登录'))
    expect(text).toBe('请重新登录')
    expect(text).not.toMatch(/csrf|CSRF|令牌|unavailable|\(/)
  })

  it('已知错误码一律映射到中文人话', () => {
    expect(errorMessage(new ApiError(401, 'invalid_credentials', 'invalid handle or password')))
      .toBe('账号或密码不正确')
    expect(errorMessage(new ApiError(429, 'rate_limited', 'too many login attempts')))
      .toBe('操作太频繁，请稍后再试')
    expect(errorMessage(new ApiError(404, 'not_found', 'not found')))
      .toBe('内容不存在或已下架')
  })

  it('后端中文可读文案保留，英文或夹带技术词的文案兜底', () => {
    expect(errorMessage(new ApiError(400, 'cover_state', '该作品还没有转码产物，暂时不能设置封面')))
      .toBe('该作品还没有转码产物，暂时不能设置封面')
    expect(errorMessage(new ApiError(500, 'internal_error', 'unexpected server error')))
      .toBe('服务暂时不可用，请稍后再试')
    expect(errorMessage(new ApiError(500, 'weird', '无法获取 CSRF 令牌')))
      .toBe('操作未完成，请稍后再试')
    expect(errorMessage(new ApiError(500, 'weird', 'HTTP 500')))
      .toBe('操作未完成，请稍后再试')
  })

  it('普通 Error 与未知异常也不把内部信息带进界面', () => {
    expect(errorMessage(new Error('加载失败'))).toBe('加载失败')
    expect(errorMessage(new Error('fetch failed'))).toBe('操作未完成，请稍后再试')
    expect(errorMessage(null)).toBe('操作未完成，请稍后再试')
    expect(errorMessage('boom')).toBe('操作未完成，请稍后再试')
  })

  it('任何返回都不含错误码括号后缀', () => {
    const codes = ['csrf_unavailable', 'http_error', 'network_error', 'invalid_input', 'unknown_code']
    for (const code of codes) {
      expect(errorMessage(new ApiError(500, code, 'x'))).not.toMatch(/（[a-z_]+）/)
    }
  })
})
