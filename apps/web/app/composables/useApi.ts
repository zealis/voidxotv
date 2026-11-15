// 同源 /api/v1 调用封装：统一 Envelope、凭据、CSRF 与错误。
// 写操作自动携带会话 CSRF 令牌；登录/注册不需要（尚未建立会话）。
export type ApiFailure = { code: string; message: string; request_id?: string }

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

type RequestOptions = {
  method?: string
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
}

type Envelope<T> = { data?: T; error?: ApiFailure }

let csrfToken = ''

async function ensureCsrf(): Promise<string> {
  if (csrfToken) return csrfToken
  const response = await fetch('/api/v1/auth/csrf', { credentials: 'include' })
  const payload = (await response.json().catch(() => null)) as Envelope<{ csrf_token: string }> | null
  if (!response.ok || !payload?.data?.csrf_token) {
    // 面向用户的提示只说该做什么；CSRF、错误码属于排障信息，不进界面。
    throw new ApiError(response.status, 'csrf_unavailable', '请重新登录')
  }
  csrfToken = payload.data.csrf_token
  return csrfToken
}

export function useApi() {
  async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const method = (options.method || 'GET').toUpperCase()
    const headers: Record<string, string> = { ...(options.headers || {}) }
    const isWrite = !['GET', 'HEAD', 'OPTIONS'].includes(method)
    const unauthenticated = path === '/api/v1/auth/register' || path === '/api/v1/auth/login'
    if (isWrite && !unauthenticated) {
      headers['X-CSRF-Token'] = await ensureCsrf()
    }
    if (options.body !== undefined) {
      headers['Content-Type'] = 'application/json'
    }

    const body = options.body === undefined ? undefined : JSON.stringify(options.body)
    const send = () => fetch(path, {
      method, headers, credentials: 'include', body, signal: options.signal,
    })
    let response = await send()
    let payload = (await response.json().catch(() => null)) as Envelope<T> | null

    // 敏感操作（删除作品 / 评论 / 图片 / 头像）：服务端要求二次确认时，
    // 弹密码框拿 5 分钟令牌，然后自动重试一次原请求。
    if (response.status === 403 && payload?.error?.code === 'step_up_required') {
      const token = await useStepUp().ensure('user', '此操作需要重新验证身份')
      if (token) {
        headers['X-Step-Up-Token'] = token
        response = await send()
        payload = (await response.json().catch(() => null)) as Envelope<T> | null
      }
    }

    if (!response.ok) {
      throw new ApiError(
        response.status,
        payload?.error?.code || 'http_error',
        payload?.error?.message || '服务暂时不可用，请稍后再试',
        payload?.error?.request_id,
      )
    }
    return (payload?.data ?? null) as T
  }

  // 图片上传：multipart 直传，带真实进度。单独走 XHR 是因为 fetch 没有
  // upload progress；CSRF 与错误信封口径与 api() 保持一致。
  async function upload<T>(
    path: string,
    body: FormData,
    onProgress?: (percent: number) => void,
  ): Promise<T> {
    const token = await ensureCsrf()
    return new Promise<T>((resolve, reject) => {
      const xhr = new XMLHttpRequest()
      xhr.open('POST', path, true)
      xhr.withCredentials = true
      xhr.setRequestHeader('X-CSRF-Token', token)
      if (onProgress) {
        xhr.upload.onprogress = (event) => {
          if (event.lengthComputable) {
            onProgress(Math.min(100, Math.round((event.loaded / event.total) * 100)))
          }
        }
      }
      xhr.onload = () => {
        let payload: Envelope<T> | null = null
        try { payload = JSON.parse(xhr.responseText) } catch { /* 非 JSON 响应当错误处理 */ }
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve((payload?.data ?? null) as T)
        } else {
          reject(new ApiError(xhr.status, payload?.error?.code || 'http_error',
            payload?.error?.message || '服务暂时不可用，请稍后再试', payload?.error?.request_id))
        }
      }
      xhr.onerror = () => reject(new ApiError(0, 'network_error', '网络异常，上传失败'))
      xhr.send(body)
    })
  }

  async function getMe(): Promise<{ user_id: number; handle: string; roles: string[] } | null> {
    try {
      return await api<{ user_id: number; handle: string; roles: string[] }>('/api/v1/auth/me')
    } catch {
      return null
    }
  }

  async function logout(): Promise<void> {
    await api('/api/v1/auth/logout', { method: 'POST' })
    csrfToken = ''
  }

  function clearCsrf(): void {
    csrfToken = ''
  }

  return { api, upload, getMe, logout, clearCsrf }
}

// 面向用户的错误文案层：错误码、HTTP 状态、英文内部错误只用于代码判断与日志，
// 一律不进界面。后端已给中文可读文案时优先用它（例如「该作品还没有转码产物」），
// 其余按错误码映射，未知的一律兜底成一句人话。
const FALLBACK_MESSAGE = '操作未完成，请稍后再试'

const USER_MESSAGES: Record<string, string> = {
  csrf_unavailable: '请重新登录',
  csrf_failed: '页面已过期，请刷新后重试',
  invalid_credentials: '账号或密码不正确',
  user_exists: '该账号已被注册，请换一个',
  account_disabled: '账号暂不可用，如有疑问请联系客服',
  invalid_input: '提交的信息有误，请检查后重试',
  invalid_json: '提交的信息有误，请检查后重试',
  invalid_id: '内容不存在或已下架',
  invalid_code: '验证码不正确或已失效',
  login_required: '请先登录',
  unauthorized: '请先登录',
  forbidden: '暂时无法执行此操作',
  not_found: '内容不存在或已下架',
  not_appealable: '该内容当前无法申诉',
  appeal_exists: '申诉已提交，请等待处理结果',
  self_follow: '不能关注自己',
  not_enabled: '该功能暂未开放',
  unsupported: '暂不支持该操作',
  conflict: '操作有冲突，请刷新后重试',
  rate_limited: '操作太频繁，请稍后再试',
  captcha_required: '请先完成安全验证',
  search_rate_limited: '搜索太频繁，请稍后再试',
  query_too_short: '请输入更多关键词',
  network_error: '网络连接异常，请检查网络后重试',
  http_error: FALLBACK_MESSAGE,
  internal_error: '服务暂时不可用，请稍后再试',
}

// 含中文且不带技术英文的后端文案可以直接展示；纯英文/夹带术语的一律兜底。
const TECHNICAL_WORD = /HTTP|CSRF|token|sql|undefined|null|internal|unexpected|invalid|timeout|EOF/i
function readable(message: string): string {
  return message && /[\u4e00-\u9fff]/.test(message) && !TECHNICAL_WORD.test(message)
    ? message
    : FALLBACK_MESSAGE
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return USER_MESSAGES[error.code] || readable(error.message)
  }
  return error instanceof Error ? readable(error.message) : FALLBACK_MESSAGE
}
