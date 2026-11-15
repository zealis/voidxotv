<script setup lang="ts">
import {
  ElButton, ElDialog, ElEmpty, ElInput, ElMessage, ElOption, ElSelect,
  ElTable, ElTableColumn,
} from 'element-plus'
import type { AdminReportCase } from '~/composables/useAdminApi'

const { request, can } = useAdminApi()
const loading = ref(true)
const error = ref('')
const items = ref<AdminReportCase[]>([])

const dialog = reactive({
  open: false,
  item: null as AdminReportCase | null,
  outcome: 'upheld' as 'upheld' | 'dismissed',
  linkedAction: 'none',
  resolution: '',
  pending: false,
  error: '',
})

const reasonLabels: Record<string, string> = {
  vulgar: '低俗', copyright: '侵权', harassment: '骚扰', fraud: '诈骗', other: '其他',
}
type Tone = 'positive' | 'attention' | 'danger' | 'neutral'
const statusMeta: Record<string, { text: string; tone: Tone }> = {
  pending: { text: '待处理', tone: 'attention' },
  upheld: { text: '已成立', tone: 'positive' },
  dismissed: { text: '不成立', tone: 'neutral' },
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ items: AdminReportCase[] }>('/api/v1/admin/reports?limit=50')
    items.value = data.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openDialog(raw: unknown) {
  const item = raw as AdminReportCase
  dialog.open = true
  dialog.item = item
  dialog.outcome = 'upheld'
  dialog.linkedAction = 'none'
  dialog.resolution = ''
  dialog.error = ''
}

async function confirm() {
  if (!dialog.item) return
  if (!dialog.resolution.trim()) {
    dialog.error = '处置说明必填，将反馈给举报人'
    return
  }
  dialog.pending = true
  try {
    await request(`/api/v1/admin/reports/${dialog.item.id}/resolve`, {
      method: 'POST',
      body: {
        outcome: dialog.outcome,
        linked_action: dialog.linkedAction === 'none' ? '' : dialog.linkedAction,
        resolution: dialog.resolution.trim(),
      },
      csrf: true,
    })
    ElMessage.success('举报单已处置')
    dialog.open = false
    await load()
  } catch (cause) {
    dialog.error = cause instanceof Error ? cause.message : '处置失败'
  } finally {
    dialog.pending = false
  }
}

const contentActions = [
  { value: 'none', label: '不联动内容' },
  { value: 'take_down', label: '下架内容' },
  { value: 'limit', label: '限流内容' },
]
const userActions = [
  { value: 'none', label: '不联动用户' },
  { value: 'warn', label: '警告用户' },
  { value: 'mute', label: '禁言用户' },
  { value: 'ban', label: '封禁用户' },
]
const commentActions = [
  { value: 'none', label: '不联动评论' },
  { value: 'remove_comment', label: '移除这条评论' },
]
const typeLabels: Record<string, string> = {
  content: '作品', comment: '评论', user: '用户',
}
const linkedOptions = computed(() => {
  const type = dialog.item?.object_type
  if (type === 'user') return userActions
  if (type === 'comment') return commentActions
  return contentActions
})

// 表格插槽给的是 element-plus 的 DefaultRow，按字段传而不是整行对象，
// 避免把弱类型行直接断言成 AdminReportCase。
function objectLabel(type: string, title: string): string {
  const text = title || '（对象已删除）'
  return type === 'comment' ? `评论：${text}` : text
}
function typeLabel(type: string): string {
  return typeLabels[type] || '对象'
}
</script>

<template>
  <section class="reports">
    <header class="toolbar">
      <ElButton @click="load">刷新</ElButton>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>
    <ElEmpty v-else-if="!loading && !items.length" description="暂无举报单" :image-size="96" />

    <ElTable v-else v-loading="loading" :data="items" size="default" class="admin-table report-table">
      <ElTableColumn label="对象" min-width="200">
        <template #default="scope">
          <div class="obj">
            <a
              v-if="scope.row.object_path" :href="scope.row.object_path"
              target="_blank" rel="noopener noreferrer"
            >{{ objectLabel(scope.row.object_type, scope.row.object_title) }}</a>
            <span v-else>{{ objectLabel(scope.row.object_type, scope.row.object_title) }}</span>
          </div>
          <div class="sub">{{ typeLabel(scope.row.object_type) }} #{{ scope.row.object_id }}</div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="举报次数" width="96">
        <template #default="scope">
          <span :class="['count', { hot: scope.row.report_count >= 3 }]">{{ scope.row.report_count }}</span>
        </template>
      </ElTableColumn>
      <ElTableColumn label="主要理由" width="96">
        <template #default="scope">{{ reasonLabels[scope.row.top_reason] || scope.row.top_reason }}</template>
      </ElTableColumn>
      <ElTableColumn label="状态" width="96">
        <template #default="scope">
          <AdminStatusTag
            :text="statusMeta[scope.row.status]?.text || scope.row.status"
            :tone="statusMeta[scope.row.status]?.tone"
          />
        </template>
      </ElTableColumn>
      <ElTableColumn label="开单时间" width="160">
        <template #default="scope">{{ new Date(scope.row.opened_at).toLocaleString('zh-CN', { hour12: false }) }}</template>
      </ElTableColumn>
      <ElTableColumn label="处置说明" min-width="160">
        <template #default="scope">
          <span class="sub">{{ scope.row.resolution || '—' }}</span>
        </template>
      </ElTableColumn>
      <ElTableColumn label="操作" width="110" fixed="right">
        <template #default="scope">
          <ElButton
            v-if="can('reports:resolve') && scope.row.status === 'pending'"
            size="small" type="primary" @click="openDialog(scope.row)"
          >处置</ElButton>
          <span v-else class="sub">{{ scope.row.status === 'pending' ? '无权限' : '已处置' }}</span>
        </template>
      </ElTableColumn>
    </ElTable>

    <!-- 窄屏卡片：对象标题可跳转，操作按钮不再被 fixed 列挤扁。 -->
    <div v-if="!error && items.length" class="admin-cards">
      <article v-for="row in items" :key="row.id" class="admin-card">
        <header class="admin-card-head">
          <div>
            <div class="admin-card-title">
              <a
                v-if="row.object_path" :href="row.object_path"
                target="_blank" rel="noopener noreferrer"
              >{{ objectLabel(row.object_type, row.object_title) }}</a>
              <span v-else>{{ objectLabel(row.object_type, row.object_title) }}</span>
            </div>
            <div class="admin-card-sub">{{ typeLabel(row.object_type) }} #{{ row.object_id }}</div>
          </div>
          <div class="admin-card-tags">
            <AdminStatusTag
              :text="statusMeta[row.status]?.text || row.status"
              :tone="statusMeta[row.status]?.tone"
            />
          </div>
        </header>
        <dl class="admin-card-meta">
          <div><dt>举报次数</dt><dd>{{ row.report_count }}</dd></div>
          <div><dt>主要理由</dt><dd>{{ reasonLabels[row.top_reason] || row.top_reason }}</dd></div>
          <div>
            <dt>开单</dt>
            <dd>{{ new Date(row.opened_at).toLocaleString('zh-CN', { hour12: false }) }}</dd>
          </div>
          <div><dt>处置说明</dt><dd>{{ row.resolution || '—' }}</dd></div>
        </dl>
        <div class="admin-card-actions">
          <ElButton
            v-if="can('reports:resolve') && row.status === 'pending'"
            size="small" type="primary" @click="openDialog(row)"
          >处置</ElButton>
          <span v-else class="admin-card-sub">{{ row.status === 'pending' ? '无权限' : '已处置' }}</span>
        </div>
      </article>
    </div>

    <ElDialog v-model="dialog.open" title="处置举报单" width="480px">
      <div class="dialog-body">
        <p class="dialog-tip">
          对象：{{ dialog.item ? objectLabel(dialog.item.object_type, dialog.item.object_title) : '（已删除）' }} ·
          累计 {{ dialog.item?.report_count }} 次举报
        </p>
        <label>结论</label>
        <ElSelect v-model="dialog.outcome" class="full">
          <ElOption value="upheld" label="举报成立" />
          <ElOption value="dismissed" label="举报不成立" />
        </ElSelect>
        <label>联动动作</label>
        <ElSelect v-model="dialog.linkedAction" class="full">
          <ElOption
            v-for="option in linkedOptions" :key="option.value"
            :value="option.value" :label="option.label"
          />
        </ElSelect>
        <label>处置说明</label>
        <ElInput
          v-model="dialog.resolution" type="textarea" :rows="4" maxlength="200"
          show-word-limit placeholder="说明处置依据与结果（必填）"
        />
        <p v-if="dialog.error" class="dialog-error">{{ dialog.error }}</p>
      </div>
      <template #footer>
        <ElButton @click="dialog.open = false">取消</ElButton>
        <ElButton type="primary" :loading="dialog.pending" @click="confirm">提交处置</ElButton>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.reports { display: grid; gap: 14px; }
.obj a {
  color: inherit;
  text-decoration: underline;
  text-underline-offset: 2px;
}
.obj a:hover { opacity: 0.8; }
.toolbar { display: flex; align-items: flex-start; justify-content: flex-end; gap: 16px; }
.state {
  display: grid; gap: 12px; place-items: start; padding: 24px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.07);
  font-size: 13px; color: #a1a1aa;
}
.state p { margin: 0; }
.report-table { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; }
.obj { font-size: 13px; font-weight: 600; color: #f4f4f5; }
.sub { font-size: 11px; color: #8b8b93; }
.count { font-weight: 700; color: #d4d4d8; }
.count.hot { color: #ff5c97; }
.dialog-body { display: grid; gap: 8px; }
.dialog-tip { margin: 0 0 4px; font-size: 12px; color: #a1a1aa; }
.dialog-body label { font-size: 12px; color: #8b8b93; margin-top: 4px; }
.full { width: 100%; }
.dialog-error { margin: 4px 0 0; font-size: 12px; color: #fb7185; }
</style>
