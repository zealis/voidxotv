// 敏感操作的二次确认：重新输入登录密码，换一个 5 分钟内有效的确认令牌。
// 令牌只放内存（useState），弹窗的等待回调放模块变量——函数不能被 SSR 序列化。

export type StepUpScope = 'user' | 'admin'

type StepUpToken = { token: string; expiresAt: number }

interface StepUpState {
  open: boolean
  scope: StepUpScope
  reason: string
  tokens: Record<StepUpScope, StepUpToken>
}

let pendingResolvers: Array<(token: string | null) => void> = []

function settle(token: string | null) {
  const list = pendingResolvers
  pendingResolvers = []
  for (const resolve of list) resolve(token)
}

export function useStepUp() {
  const state = useState<StepUpState>('stepup', () => ({
    open: false,
    scope: 'user',
    reason: '',
    tokens: { user: { token: '', expiresAt: 0 }, admin: { token: '', expiresAt: 0 } },
  }))

  function cached(scope: StepUpScope): string {
    const item = state.value.tokens[scope]
    return item && item.expiresAt > Date.now() ? item.token : ''
  }

  // 有缓存就直接用；没有就弹窗等用户输密码。并发调用共用同一个弹窗。
  function ensure(scope: StepUpScope, reason: string): Promise<string | null> {
    const token = cached(scope)
    if (token) return Promise.resolve(token)
    if (!state.value.open) {
      state.value.scope = scope
      state.value.reason = reason
      state.value.open = true
    }
    return new Promise((resolve) => { pendingResolvers.push(resolve) })
  }

  function succeed(scope: StepUpScope, token: string, expiresIn: number) {
    state.value.tokens[scope] = {
      token,
      // 提前 20 秒失效，避免刚好卡在边界上被后端拒绝。
      expiresAt: Date.now() + Math.max(30, expiresIn - 20) * 1000,
    }
    state.value.open = false
    settle(token)
  }

  function cancel() {
    state.value.open = false
    settle(null)
  }

  // 退出登录 / 改密后清空，避免旧令牌继续可用。
  function clear() {
    state.value.tokens = { user: { token: '', expiresAt: 0 }, admin: { token: '', expiresAt: 0 } }
  }

  return { state, ensure, succeed, cancel, clear, cached }
}
