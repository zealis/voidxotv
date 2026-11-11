<script setup lang="ts">
import {
  ElButton, ElEmpty, ElInput, ElInputNumber, ElMessage, ElOption, ElSelect, ElTable, ElTableColumn,
} from 'element-plus'
import type {
  AdminAuditEntry, AdminConnector, AdminImportJob,
} from '~/composables/useAdminApi'

interface SettingsPayload {
  storage: { endpoint: string; bucket: string; public_read: boolean; keys_configured: boolean }
  delivery: {
    nuxt_origin: string; token_configured: boolean; debounce: string
    home_debounce: string; cdn_purge_configured: boolean
  }
  transcoding: {
    short_video_max_ms: number; max_upload_mb: number
    ffmpeg_configured: boolean; profile_version: string
  }
  site: { site_url: string; environment: string }
  risk_weights: { report: number; machine: number; copyright: number }
  hot_weights: { play_weight: number; like_weight: number }
  diagnostics: {
    connectors: number; pending_imports: number; pending_reviews: number
    pending_reports: number; stale_pages: number
    delivery_jobs: Record<string, number>
    outbox_events: Record<string, number>
  }
}

// Outbox 事件视图：失败事件需要人工判断「重放」还是「内容已删、放弃」。
interface AdminOutboxEvent {
  id: number
  event_key: string
  event_type: string
  status: string
  attempts: number
  last_error?: string
  content_id?: number
  slug?: string
  created_at: string
  processed_at?: string | null
}

const { request, can } = useAdminApi()
const loading = ref(true)
const error = ref('')
const settings = ref<SettingsPayload | null>(null)
// 二次验证（§11.6）与热门榜权重（§7.6）的本地状态。
const totpEnabled = ref(false)
const totpDraft = ref<{ secret: string; otpauth_url: string } | null>(null)
const totpCode = ref('')
const totpBusy = ref(false)
const hotPlay = ref(1)
const hotLike = ref(3)
const weightsBusy = ref(false)
const connectors = ref<AdminConnector[]>([])
const imports = ref<AdminImportJob[]>([])
const audit = ref<AdminAuditEntry[]>([])
// 审计日志：动作前缀过滤 + 游标分页（next_cursor 来自服务端）。
const AUDIT_FILTERS = [
  { value: '', label: '全部动作' },
  { value: 'review.', label: '内容审核' },
  { value: 'user.', label: '用户处置' },
  { value: 'report.', label: '举报' },
  { value: 'settings.', label: '系统设置' },
  { value: 'stats.', label: '统计' },
  { value: 'totp.', label: '二次验证' },
]
const auditFilter = ref('')
const auditCursor = ref<number | null>(null)
const auditLoading = ref(false)

async function loadAudit(reset = true) {
  auditLoading.value = true
  try {
    const params = new URLSearchParams({ limit: '20' })
    if (auditFilter.value) params.set('action', auditFilter.value)
    if (!reset && auditCursor.value) params.set('cursor', String(auditCursor.value))
    const data = await request<{ items: AdminAuditEntry[]; next_cursor: number | null }>(
      `/api/v1/admin/audit?${params.toString()}`)
    audit.value = reset ? data.items : [...audit.value, ...data.items]
    auditCursor.value = data.next_cursor
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '审计日志加载失败')
  } finally {
    auditLoading.value = false
  }
}
const outboxFailed = ref<AdminOutboxEvent[]>([])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [settingsData, connectorData, importData, auditData, outboxData] = await Promise.all([
      request<SettingsPayload>('/api/v1/admin/settings'),
      request<{ items: AdminConnector[] }>('/api/v1/admin/connectors'),
      request<{ items: AdminImportJob[] }>('/api/v1/admin/imports?limit=10'),
      Promise.resolve({ items: [] as AdminAuditEntry[] }),
      can('delivery:admin')
        ? request<{ items: AdminOutboxEvent[] }>('/api/v1/admin/outbox?status=failed&limit=10')
        : Promise.resolve({ items: [] as AdminOutboxEvent[] }),
    ])
    settings.value = settingsData
    try {
      const totp = await request<{ enabled: boolean }>('/api/v1/admin/totp')
      totpEnabled.value = totp.enabled
    } catch { /* 权限不足时保持未启用显示 */ }
    if (settingsData.hot_weights) {
      hotPlay.value = settingsData.hot_weights.play_weight
      hotLike.value = settingsData.hot_weights.like_weight
    }
    connectors.value = connectorData.items || []
    imports.value = importData.items || []
    audit.value = []
    outboxFailed.value = outboxData.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
  if (can('audit:read')) await loadAudit(true)
}

onMounted(load)

// ── 二次验证（§11.6）：setup 返回密钥，验证一次动态码后才落库生效 ──
async function totpSetup() {
  totpBusy.value = true
  try {
    totpDraft.value = await request<{ secret: string; otpauth_url: string }>(
      '/api/v1/admin/totp/setup', { method: 'POST', body: {}, csrf: true })
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '生成密钥失败')
  } finally {
    totpBusy.value = false
  }
}

async function totpConfirm() {
  if (!totpDraft.value) return
  totpBusy.value = true
  try {
    await request('/api/v1/admin/totp/enable', {
      method: 'POST', body: { secret: totpDraft.value.secret, code: totpCode.value.trim() }, csrf: true,
    })
    totpEnabled.value = true
    totpDraft.value = null
    totpCode.value = ''
    ElMessage.success('二次验证已启用，下次登录需输入动态码')
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '验证失败')
  } finally {
    totpBusy.value = false
  }
}

// ── 热门榜权重（§7.6）：PUT 后下一次统计聚合生效 ──
async function saveWeights() {
  weightsBusy.value = true
  try {
    const result = await request<{ play_weight: number; like_weight: number }>(
      '/api/v1/admin/settings/hot-weights', {
        method: 'PUT', body: { play_weight: hotPlay.value, like_weight: hotLike.value }, csrf: true,
      })
    if (settings.value) settings.value.hot_weights = result
    ElMessage.success('权重已保存')
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '保存失败')
  } finally {
    weightsBusy.value = false
  }
}

const blocks = computed(() => {
  const data = settings.value
  if (!data) return []
  return [
    {
      title: '存储', rows: [
        ['端点', data.storage.endpoint || '未配置'],
        ['桶', data.storage.bucket || '未配置'],
        ['匿名公共读', data.storage.public_read ? '开启' : '关闭（签名 URL）'],
        ['访问密钥', data.storage.keys_configured ? '服务端已配置' : '缺失'],
      ],
    },
    {
      title: '页面交付', rows: [
        ['Nuxt 内部地址', data.delivery.nuxt_origin],
        ['内部凭证', data.delivery.token_configured ? '服务端已配置' : '缺失'],
        ['防抖', `${data.delivery.debounce} · 首页 ${data.delivery.home_debounce}`],
        ['CDN 精确失效', data.delivery.cdn_purge_configured ? '已配置' : '未配置（仅记录日志）'],
      ],
    },
    {
      title: '转码', rows: [
        ['参数版本', data.transcoding.profile_version],
        ['短视频阈值', `${Math.round(data.transcoding.short_video_max_ms / 1000)} 秒`],
        ['最大上传', `${data.transcoding.max_upload_mb} MiB`],
        ['FFmpeg', data.transcoding.ffmpeg_configured ? '服务端已配置' : '缺失'],
      ],
    },
    {
      title: '站点与风控权重', rows: [
        ['站点地址', data.site.site_url],
        ['环境', data.site.environment],
        ['风险权重', `举报 ${data.risk_weights.report} · 机器 ${data.risk_weights.machine} · 版权 ${data.risk_weights.copyright}`],
      ],
    },
  ]
})

async function refreshConnector(raw: unknown) {
  const connector = raw as AdminConnector
  try {
    await request(`/api/v1/admin/connectors/${connector.id}/refresh`, {
      method: 'POST', body: {}, csrf: true,
    })
    ElMessage.success('已记录刷新时间')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '刷新失败')
  }
}

async function retryDelivery() {
  try {
    const result = await request<{ retried: number }>('/api/v1/admin/delivery/retry', {
      method: 'POST', body: {}, csrf: true,
    })
    ElMessage.success(result.retried
      ? `已重新入队 ${result.retried} 个失败任务`
      : '没有需要重试的失败任务')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '重试失败')
  }
}

type Tone = 'positive' | 'attention' | 'danger' | 'neutral'
const outboxStatusText: Record<string, string> = {
  pending: '待处理', processing: '处理中', done: '已完成', failed: '失败',
}

async function retryOutbox(id?: number) {
  try {
    const result = await request<{ retried: number }>('/api/v1/admin/outbox/retry', {
      method: 'POST', body: id ? { ids: [id] } : {}, csrf: true,
    })
    ElMessage.success(result.retried
      ? `已重放 ${result.retried} 条事件`
      : '没有需要重放的事件')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '重放失败')
  }
}

const connectorStatus: Record<string, { text: string; tone: Tone }> = {
  active: { text: '已授权', tone: 'positive' },
  disabled: { text: '已停用', tone: 'neutral' },
  unauthorized: { text: '未授权', tone: 'attention' },
  error: { text: '异常', tone: 'danger' },
}

const jobStatusText: Record<string, string> = {
  pending: '排队中', downloading: '下载中', transcoding: '转码中',
  published: '已发布', failed: '失败',
}

function formatBytes(value: number): string {
  if (!value) return '—'
  if (value >= 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MiB`
  return `${(value / 1024).toFixed(0)} KiB`
}
</script>

<template>
  <section class="settings">
    <header class="toolbar">
      <ElButton @click="load">刷新</ElButton>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>

    <template v-else>
      <div v-loading="loading" class="blocks">
        <article v-for="block in blocks" :key="block.title" class="block">
          <h3>{{ block.title }}</h3>
          <dl>
            <template v-for="row in block.rows" :key="row[0]">
              <dt>{{ row[0] }}</dt>
              <dd>{{ row[1] }}</dd>
            </template>
          </dl>
        </article>
      </div>

      <section v-if="settings" class="panel">
        <header class="panel-head">
          <h3>运行诊断</h3>
          <span class="badge">页面与任务状态</span>
        </header>
        <div class="diagnostics">
          <div><strong>{{ settings.diagnostics.pending_reviews }}</strong><span>待审内容</span></div>
          <div><strong>{{ settings.diagnostics.pending_reports }}</strong><span>待处理举报</span></div>
          <div><strong>{{ settings.diagnostics.pending_imports }}</strong><span>进行中导入</span></div>
          <div><strong>{{ settings.diagnostics.stale_pages }}</strong><span>待再生成页面</span></div>
          <div><strong>{{ settings.diagnostics.connectors }}</strong><span>来源 Connector</span></div>
        </div>
        <p class="jobs">
          Delivery 任务：
          <span v-for="(count, status) in settings.diagnostics.delivery_jobs" :key="status">
            {{ status }} {{ count }}
          </span>
          <span v-if="!Object.keys(settings.diagnostics.delivery_jobs).length">暂无任务</span>
        </p>
        <div v-if="can('delivery:admin')" class="diagnostic-actions">
          <ElButton size="small" @click="retryDelivery">重试失败任务</ElButton>
          <span class="sub">仅重置任务状态，不修改业务数据</span>
        </div>
      </section>

      <!-- 二次验证（§11.6 应启用 TOTP）与热门榜权重（§7.6 运行时可调） -->
      <section v-if="settings" class="panel">
        <header class="panel-head">
          <h3>安全与参数</h3>
          <span class="badge">当前账号</span>
        </header>
        <div class="totp">
          <div>
            <strong>后台二次验证（TOTP）</strong>
            <p class="sub">
              {{ totpEnabled ? '已启用：登录需输入动态码' : '未启用。用认证器 App 扫码或手动录入密钥，验证一次动态码后生效。' }}
            </p>
          </div>
          <ElButton size="small" type="primary" @click="totpSetup">
            {{ totpEnabled ? '更换密钥' : '启用二次验证' }}
          </ElButton>
        </div>
        <div v-if="totpDraft" class="totp-setup">
          <p class="sub">
            密钥：<code>{{ totpDraft.secret }}</code>
            （ otpauth 链接：
            <code style="word-break: break-all">{{ totpDraft.otpauth_url }}</code> ）
          </p>
          <div class="totp-confirm">
            <ElInput
              v-model="totpCode" inputmode="numeric" maxlength="6"
              placeholder="输入 App 显示的 6 位动态码" style="max-width: 240px"
            />
            <ElButton size="small" type="primary" :loading="totpBusy" @click="totpConfirm">
              验证并生效
            </ElButton>
          </div>
        </div>
        <div class="hot-weights">
          <strong>热门榜聚合权重（§7.6）</strong>
          <div class="weights">
            <label>播放权重 <ElInputNumber v-model="hotPlay" :min="0" :max="1000" :step="0.5" /></label>
            <label>点赞权重 <ElInputNumber v-model="hotLike" :min="0" :max="1000" :step="0.5" /></label>
            <ElButton size="small" :loading="weightsBusy" @click="saveWeights">保存权重</ElButton>
          </div>
          <p class="sub">下一次统计聚合（{{ settings.transcoding.profile_version }} 周期）生效。</p>
        </div>
      </section>

      <!-- Outbox 事件：失败事件必须可见、可重放，否则只能手工改库（§10.4） -->
      <section v-if="settings" class="panel">
        <header class="panel-head">
          <h3>Outbox 事件</h3>
          <span class="badge">写路径 → 页面失效</span>
        </header>
        <p class="jobs">
          <span v-for="(count, status) in settings.diagnostics.outbox_events" :key="status">
            {{ outboxStatusText[status] || status }} {{ count }}
          </span>
          <span v-if="!Object.keys(settings.diagnostics.outbox_events || {}).length">暂无事件</span>
        </p>

        <template v-if="outboxFailed.length">
          <ul class="outbox-list">
            <li v-for="event in outboxFailed" :key="event.id">
              <div class="outbox-main">
                <div class="strong">
                  #{{ event.id }} · {{ event.event_type }}
                  <span class="sub">
                    {{ event.content_id ? `内容 ${event.content_id}` : '无内容引用' }}{{ event.slug ? ` · ${event.slug}` : '' }}
                  </span>
                </div>
                <p class="outbox-err">{{ event.last_error || '（无错误信息）' }}</p>
                <div class="sub">
                  尝试 {{ event.attempts }} 次 · {{ new Date(event.created_at).toLocaleString('zh-CN', { hour12: false }) }}
                </div>
              </div>
              <ElButton
                v-if="can('delivery:admin')" size="small" plain
                @click="retryOutbox(event.id)"
              >重放</ElButton>
            </li>
          </ul>
          <div v-if="can('delivery:admin')" class="diagnostic-actions">
            <ElButton size="small" type="primary" @click="retryOutbox()">重放全部失败事件</ElButton>
            <span class="sub">只重置事件状态；内容若已删除会再次失败</span>
          </div>
        </template>
        <p v-else class="jobs"><span>没有失败事件</span></p>
      </section>

      <!-- 分类配置：分类页与搜索筛选的数据源（§4.7 系统设置） -->
      <AdminCategories />

      <section class="panel">
        <header class="panel-head">
          <h3>外部来源 Connector</h3>
          <span class="badge">密钥仅服务端</span>
        </header>
        <ElEmpty v-if="!connectors.length" description="暂无 Connector" :image-size="72" />
        <ElTable v-else :data="connectors" size="small" class="table">
          <ElTableColumn label="名称" min-width="150">
            <template #default="scope">
              <div class="strong">{{ scope.row.name }}</div>
              <div class="sub">{{ scope.row.slug }} · {{ scope.row.kind }}</div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" width="90">
            <template #default="scope">
              <AdminStatusTag
                :text="connectorStatus[scope.row.status]?.text || scope.row.status"
                :tone="connectorStatus[scope.row.status]?.tone"
              />
            </template>
          </ElTableColumn>
          <ElTableColumn label="密钥" width="80">
            <template #default="scope">{{ scope.row.secret_configured ? '已配置' : '未配置' }}</template>
          </ElTableColumn>
          <ElTableColumn label="能力" min-width="130">
            <template #default="scope">
              <span class="sub">
                {{ scope.row.allow_direct_play ? '直连播放' : '' }}
                {{ scope.row.allow_import ? ' 下载导入' : '' }}
                {{ !scope.row.allow_direct_play && !scope.row.allow_import ? '未授权' : '' }}
              </span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="最近刷新" width="150">
            <template #default="scope">
              <span class="sub">
                {{ scope.row.last_refresh_at ? new Date(scope.row.last_refresh_at).toLocaleString('zh-CN', { hour12: false }) : '—' }}
              </span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="操作" width="90">
            <template #default="scope">
              <ElButton
                v-if="can('connectors:write')" size="small" plain
                @click="refreshConnector(scope.row)"
              >刷新</ElButton>
            </template>
          </ElTableColumn>
        </ElTable>
      </section>

      <section class="panel">
        <header class="panel-head">
          <h3>导入任务</h3>
          <span class="badge">下载与转码由 Worker 执行</span>
        </header>
        <ElEmpty v-if="!imports.length" description="暂无导入任务" :image-size="72" />
        <ElTable v-else :data="imports" size="small" class="table">
          <ElTableColumn prop="id" label="#" width="56" />
          <ElTableColumn label="外部 ID" min-width="140">
            <template #default="scope">
              <div class="strong">{{ scope.row.external_id || '—' }}</div>
              <div class="sub">{{ scope.row.source_url || '由 Connector 解析' }}</div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" width="90">
            <template #default="scope">{{ jobStatusText[scope.row.status] || scope.row.status }}</template>
          </ElTableColumn>
          <ElTableColumn label="大小" width="90">
            <template #default="scope">{{ formatBytes(scope.row.byte_size) }}</template>
          </ElTableColumn>
          <ElTableColumn label="错误" min-width="150">
            <template #default="scope"><span class="sub">{{ scope.row.last_error || '—' }}</span></template>
          </ElTableColumn>
        </ElTable>
      </section>

      <section v-if="can('audit:read')" class="panel">
        <header class="panel-head">
          <h3>审计日志</h3>
          <span class="badge">只读 · 保留 ≥ 1 年</span>
        </header>
        <div class="audit-filter">
          <ElSelect v-model="auditFilter" size="small" style="width: 180px" @change="loadAudit(true)">
            <ElOption
              v-for="item in AUDIT_FILTERS" :key="item.value"
              :label="item.label" :value="item.value"
            />
          </ElSelect>
          <ElButton size="small" :disabled="!auditCursor" @click="loadAudit(false)">加载更多</ElButton>
        </div>
        <ElEmpty v-if="!audit.length" description="该筛选下暂无审计记录" :image-size="72" />
        <ElTable v-else :data="audit" size="small" class="table">
          <ElTableColumn label="时间" width="160">
            <template #default="scope">
              {{ new Date(scope.row.created_at).toLocaleString('zh-CN', { hour12: false }) }}
            </template>
          </ElTableColumn>
          <ElTableColumn prop="username" label="管理员" width="130" />
          <ElTableColumn prop="role" label="角色" width="110" />
          <ElTableColumn prop="action" label="动作" min-width="140" />
          <ElTableColumn label="对象" min-width="120">
            <template #default="scope">{{ scope.row.object_type }} #{{ scope.row.object_id }}</template>
          </ElTableColumn>
        </ElTable>
      </section>
    </template>
  </section>
</template>

<style scoped>
.settings { display: grid; gap: 16px; }
.toolbar { display: flex; align-items: flex-start; justify-content: flex-end; gap: 16px; }
.audit-filter { display: flex; gap: 10px; justify-content: flex-end; margin: 0 0 10px; }
.state {
  display: grid; gap: 12px; place-items: start; padding: 24px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.07);
  font-size: 13px; color: #a1a1aa;
}
.state p { margin: 0; }
.blocks {
  display: grid; gap: 12px;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
}
.block {
  padding: 16px 18px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07);
}
.block h3 { margin: 0 0 10px; font-size: 14px; color: #f4f4f5; }
dl { margin: 0; display: grid; grid-template-columns: 96px 1fr; gap: 6px 10px; }
dt { font-size: 12px; color: #8b8b93; }
dd { margin: 0; font-size: 12px; color: #e4e4e7; word-break: break-all; }
.panel {
  padding: 16px 18px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07);
}
.panel-head {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: 10px;
}
.panel-head h3 { margin: 0; font-size: 14px; color: #f4f4f5; }
.badge {
  padding: 2px 8px; border-radius: 99px; font-size: 11px;
  background: rgba(34, 211, 238, 0.12); color: #67e8f9;
}
.diagnostics {
  display: grid; gap: 10px;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
}
.diagnostics div {
  padding: 10px 12px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.03);
  display: grid; gap: 2px;
}
.diagnostics strong { font-size: 20px; color: #fafafa; }
.diagnostics span { font-size: 11px; color: #8b8b93; }
.jobs { margin: 10px 0 0; font-size: 12px; color: #a1a1aa; display: flex; gap: 10px; flex-wrap: wrap; }
.outbox-list { list-style: none; margin: 10px 0 0; padding: 0; display: grid; gap: 8px; }
.outbox-list li {
  display: flex; gap: 12px; align-items: flex-start;
  padding: 10px 12px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.03);
}
.outbox-main { flex: 1; min-width: 0; display: grid; gap: 4px; }
.outbox-err { margin: 0; font-size: 11px; color: #fca5a5; word-break: break-all; }
.diagnostic-actions { margin-top: 10px; display: flex; align-items: center; gap: 10px; }
.table { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; }
.strong { font-size: 12px; color: #f4f4f5; }
.sub { font-size: 11px; color: #8b8b93; }
</style>
