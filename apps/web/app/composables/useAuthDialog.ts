// 全站登录 / 注册弹窗：前台入口统一走这里，不再跳 /login 独立页。
// 状态放 useState：SSR 每请求独立，客户端跨组件共享同一份；
// /login 页面保留作深链接兜底（邮件、书签、无 JS 场景）。
export type AuthMode = 'login' | 'register'

export interface AuthDialogOptions {
  /** 标题下的一句话，说明为什么需要登录 */
  reason?: string
  /** 登录成功后跳转的站内路径；空串表示留在当前页并原地刷新数据 */
  redirect?: string
}

interface AuthDialogState extends AuthDialogOptions {
  open: boolean
  mode: AuthMode
  /**
   * 唤起时所在页面的表面类型。弹窗挂在 app.vue、在 [data-surface] 容器之外，
   * 不显式标记的话拿不到工具页的浅色令牌，只会在 :root 深色默认值上渲染。
   */
  surface: 'tool' | 'immersive'
}

export function useAuthDialog() {
  const state = useState<AuthDialogState>('vp-auth-dialog', () => ({
    open: false,
    mode: 'login',
    reason: '',
    redirect: '',
    surface: 'immersive',
  }))

  function currentSurface(): 'tool' | 'immersive' {
    if (!import.meta.client) return 'immersive'
    const host = document.querySelector<HTMLElement>('[data-surface]')
    const value = host?.dataset.surface
    return value === 'tool' ? 'tool' : 'immersive'
  }

  function open(mode: AuthMode = 'login', options: AuthDialogOptions = {}) {
    state.value = {
      open: true,
      mode,
      reason: options.reason ?? '',
      redirect: options.redirect ?? '',
      surface: currentSurface(),
    }
  }

  function setMode(mode: AuthMode) {
    state.value = { ...state.value, mode }
  }

  function close() {
    state.value = { ...state.value, open: false }
  }

  return { state, open, setMode, close }
}
