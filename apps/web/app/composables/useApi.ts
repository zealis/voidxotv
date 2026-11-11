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
    throw new ApiError(response.status, 'csrf_unavailable', '无法获取 CSRF 令牌，请重新登录')
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

    const response = await fetch(path, {
      method,
      headers,
      credentials: 'include',
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    })
    const payload = (await response.json().catch(() => null)) as Envelope<T> | null
    if (!response.ok) {
      throw new ApiError(
        response.status,
        payload?.error?.code || 'http_error',
        payload?.error?.message || `HTTP ${response.status}`,
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
            payload?.error?.message || `HTTP ${xhr.status}`, payload?.error?.request_id))
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

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return `${error.message}（${error.code}）`
  return error instanceof Error ? error.message : String(error)
}
