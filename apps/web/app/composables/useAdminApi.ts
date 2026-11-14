// 后台数据层：所有请求走同源 BFF（/api/v1/admin/*），
// 浏览器从不直接持有 Go API 地址；CSRF 头由会话中的 token 自动补齐。
export interface AdminSession {
  id: number
  /** 后台登录标识：2026-11 起只认邮箱，与前台账号同一口径。 */
  email: string
  role: string
  permissions: string[]
  csrf_token: string
}

export interface AdminDelta {
  value: number
  direction: 'up' | 'down' | 'flat'
  label: string
}

export interface AdminOverview {
  data_ready: boolean
  period_label: string
  updated_at: string | null
  total_plays: number
  new_users: number
  pending_reviews: number
  report_resolution_rate: number
  avg_wait_minutes: number
  deltas: Record<string, AdminDelta>
  note: string
}

export interface AdminReviewItem {
  content_id: number
  slug: string
  type: 'video' | 'image'
  title: string
  author_id: number
  handle: string
  published_at: string
  review_status: string
  risk_score: number
  report_count: number
  risk_labels: string[]
  waiting_minutes: number
  poster_url: string
}

export interface AdminTrendPoint {
  day: string
  plays: number
  new_users: number
}

export interface AdminHotItem {
  rank: number
  content_id: number
  slug: string
  title: string
  handle: string
  plays: number
  likes: number
  status: string
}

export interface AdminReportCase {
  id: number
  object_type: string
  object_id: number
  object_title: string
  status: string
  report_count: number
  top_reason: string
  resolution: string
  linked_action: string
  opened_at: string
  resolved_at: string | null
}

export interface AdminUserRow {
  id: number
  handle: string
  role: string
  status: string
  created_at: string
  last_sanction: string | null
  content_count: number
}

export interface AdminTopic {
  id: number
  slug: string
  name: string
  status: string
  content_count: number
  placement: string
  created_at: string
}

export interface AdminConnector {
  id: number
  slug: string
  name: string
  kind: string
  base_url: string
  status: string
  config: Record<string, unknown>
  secret_configured: boolean
  allow_direct_play: boolean
  allow_import: boolean
  last_refresh_at: string | null
  last_error: string
  created_at: string
}

export interface AdminImportJob {
  id: number
  connector_id: number
  external_id: string
  source_url: string
  status: string
  content_id: number | null
  byte_size: number
  last_error: string
  created_at: string
}

export interface AdminAuditEntry {
  id: number
  admin_id: number
  admin_email: string
  role: string
  action: string
  object_type: string
  object_id: string
  before_summary: unknown
  after_summary: unknown
  created_at: string
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'
  body?: unknown
  csrf?: boolean
}

export function useAdminApi() {
  const session = useState<AdminSession | null>('admin-session', () => null)
  const booting = useState<boolean>('admin-booting', () => true)

  async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = {}
    if (options.body !== undefined) headers['Content-Type'] = 'application/json'
    if (options.csrf && session.value?.csrf_token) {
      headers['X-CSRF-Token'] = session.value.csrf_token
    }
    try {
      const payload = await $fetch<{ data: T }>(path, {
        method: options.method || 'GET',
        body: options.body as Record<string, unknown> | undefined,
        headers,
        credentials: 'same-origin',
      })
      return payload.data
    } catch (error) {
      const status = (error as { status?: number; response?: { status?: number } }).status
        || (error as { response?: { status?: number } }).response?.status
      if (status === 401) session.value = null
      const body = (error as { data?: { error?: { message?: string } } }).data
      // 后台同样不向界面暴露 HTTP 状态、英文内部错误等开发术语。
      // 后台错误形如 `admin: invalid input: 中文说明`：先剥掉英文前缀再判可读性，
      // 否则中文说明会被下面的技术词过滤（invalid）连坐，只剩一句「请求失败」。
      const raw = (body?.error?.message || '').replace(/^admin:[^:]*:\s*/, '')
      const clean = raw && /[\u4e00-\u9fff]/.test(raw)
        && !/HTTP|CSRF|token|sql|undefined|null|internal|unexpected|invalid|timeout/i.test(raw)
      throw new Error(clean ? raw : '请求失败，请稍后再试')
    }
  }

  async function login(email: string, password: string, totpCode?: string): Promise<void> {
    const result = await request<AdminSession>('/api/v1/admin/auth/login', {
      method: 'POST',
      body: { email, password, totp_code: totpCode || undefined },
    })
    session.value = result
  }

  async function logout(): Promise<void> {
    try {
      await request('/api/v1/admin/auth/logout', { method: 'POST', body: {}, csrf: true })
    } finally {
      session.value = null
    }
  }

  async function loadSession(): Promise<void> {
    booting.value = true
    try {
      session.value = await request<AdminSession>('/api/v1/admin/auth/me')
    } catch {
      session.value = null
    } finally {
      booting.value = false
    }
  }

  function can(permission: string): boolean {
    return !!session.value?.permissions.includes(permission)
  }

  return { session, booting, request, login, logout, loadSession, can }
}
