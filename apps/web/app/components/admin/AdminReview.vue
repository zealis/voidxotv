<script setup lang="ts">
import {
  ElButton, ElCheckbox, ElDialog, ElEmpty, ElInput, ElMessage, ElTable, ElTableColumn,
} from 'element-plus'
import type { AdminReviewItem } from '~/composables/useAdminApi'

// focusId 预留给从概览「审核」入口定位条目；列表本身按风险分排序。
defineProps<{ focusId?: number }>()
const emit = defineEmits<{ changed: [] }>()
const { request, can } = useAdminApi()

const loading = ref(true)
const error = ref('')
const items = ref<AdminReviewItem[]>([])
const selected = ref<AdminReviewItem[]>([])
const busyId = ref<number | null>(null)
// 审核模块内分两个视图：待审队列与作者申诉（§9.4）。
const mode = ref<'queue' | 'appeals'>('queue')

const dialog = reactive({
  open: false,
  action: 'reject' as 'reject' | 'take_down',
  item: null as AdminReviewItem | null,
  reason: '',
  pending: false,
  error: '',
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ items: AdminReviewItem[] }>('/api/v1/admin/reviews?limit=50')
    items.value = data.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

function rowClass({ row }: { row: unknown }) {
  return (row as AdminReviewItem).report_count >= 3 ? 'high-risk' : ''
}

function onSelection(rows: unknown[]) {
  selected.value = (rows || []) as AdminReviewItem[]
}

// 窄屏卡片没有 ElTable 的 selection 列，用勾选框维护同一份 selected，
// 保证批量通过按钮在卡片模式下依然可用。
function onCardCheck(row: AdminReviewItem, value: unknown) {
  const rest = selected.value.filter((item) => item.content_id !== row.content_id)
  selected.value = Boolean(value) ? [...rest, row] : rest
}

async function decide(raw: unknown, action: string, reason = '') {
  const item = raw as AdminReviewItem
  busyId.value = item.content_id
  try {
    await request(`/api/v1/admin/reviews/${item.content_id}/decision`, {
      method: 'POST', body: { action, reason }, csrf: true,
    })
    ElMessage.success(action === 'approve' ? '已通过审核' : '已提交处置')
    await load()
    emit('changed')
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '操作失败')
  } finally {
    busyId.value = null
  }
}

function openReasonDialog(raw: unknown, action: 'reject' | 'take_down') {
  const item = raw as AdminReviewItem
  dialog.open = true
  dialog.action = action
  dialog.item = item
  dialog.reason = ''
  dialog.error = ''
}

async function confirmReason() {
  if (!dialog.item) return
  if (!dialog.reason.trim()) {
    dialog.error = '驳回与下架必须填写理由，理由会通知作者'
    return
  }
  dialog.pending = true
  try {
    await decide(dialog.item, dialog.action, dialog.reason.trim())
    dialog.open = false
  } finally {
    dialog.pending = false
  }
}

async function batchApprove() {
  const ids = selected.value.map((item) => item.content_id)
  if (!ids.length) return
  try {
    const result = await request<{ approved: number; requested: number }>(
      '/api/v1/admin/reviews/batch-approve',
      { method: 'POST', body: { ids }, csrf: true },
    )
    ElMessage.success(`已通过 ${result.approved} / ${result.requested} 条`)
    selected.value = []
    await load()
    emit('changed')
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '批量操作失败')
  }
}

function detailLink(raw: unknown): string {
  const item = raw as AdminReviewItem
  return item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`
}

function waitLabel(minutes: number): string {
  if (minutes < 60) return `${minutes} 分钟`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时`
  return `${Math.floor(minutes / 1440)} 天`
}

// 状态色沿用文档 §4.10 的语义：正向=青，待处理=粉，负向=红。
const reviewTone: Record<string, 'positive' | 'attention' | 'danger'> = {
  pending: 'attention', approved: 'positive', rejected: 'danger',
}
const statusText: Record<string, string> = {
  pending: '待审', approved: '已通过', rejected: '已驳回',
  taken_down: '已下架', limited: '已限流',
}
</script>

<template>
  <section class="review">
    <nav class="subtabs" aria-label="审核视图">
      <button type="button" :class="{ on: mode === 'queue' }" @click="mode = 'queue'">
        待审内容
      </button>
      <button type="button" :class="{ on: mode === 'appeals' }" @click="mode = 'appeals'">
        作者申诉
      </button>
    </nav>

    <AdminAppeals v-if="mode === 'appeals'" @changed="load" />

    <div v-else class="queue">
    <header class="toolbar">
      <div class="actions">
        <ElButton
          v-if="can('reviews:decide')" type="primary"
          :disabled="!selected.length" @click="batchApprove"
        >
          批量通过（{{ selected.length }}）
        </ElButton>
        <ElButton @click="load">刷新</ElButton>
      </div>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>
    <ElEmpty v-else-if="!loading && !items.length" description="暂无待审内容" :image-size="96" />

    <ElTable
      v-else v-loading="loading" :data="items" :row-class-name="rowClass"
      size="default" class="admin-table review-table" @selection-change="onSelection"
    >
      <ElTableColumn type="selection" width="42" />
      <ElTableColumn label="作品" min-width="240">
        <template #default="scope">
          <div class="cell">
            <img
              v-if="scope.row.poster_url" :src="scope.row.poster_url"
              :alt="scope.row.title || '待审作品'" loading="lazy"
            >
            <div v-else class="thumb">{{ scope.row.type === 'image' ? '图集' : '视频' }}</div>
            <div class="cell-body">
              <a :href="detailLink(scope.row)" target="_blank" rel="noopener" class="title">
                {{ scope.row.title || '（无标题）' }}
              </a>
              <span class="sub">@{{ scope.row.handle }} · {{ scope.row.type === 'image' ? '图集' : '视频' }}</span>
            </div>
          </div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="风险" min-width="180">
        <template #default="scope">
          <div class="tags">
            <AdminStatusTag
              v-for="label in scope.row.risk_labels" :key="label"
              :text="label" :tone="scope.row.report_count >= 3 ? 'danger' : 'attention'"
            />
            <span class="score">风险分 {{ scope.row.risk_score }}</span>
          </div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="等待" width="90">
        <template #default="scope">{{ waitLabel(scope.row.waiting_minutes) }}</template>
      </ElTableColumn>
      <ElTableColumn label="状态" width="86">
        <template #default="scope">
          <AdminStatusTag
            :text="statusText[scope.row.review_status] || scope.row.review_status"
            :tone="reviewTone[scope.row.review_status]"
          />
        </template>
      </ElTableColumn>
      <ElTableColumn label="操作" width="310" fixed="right">
        <template #default="scope">
          <div class="row-actions">
            <ElButton
              v-if="can('reviews:decide')" size="small" type="primary" plain
              :loading="busyId === scope.row.content_id"
              @click="decide(scope.row, 'approve')"
            >通过</ElButton>
            <ElButton
              v-if="can('reviews:decide')" size="small" type="danger" plain
              @click="openReasonDialog(scope.row, 'reject')"
            >驳回</ElButton>
            <ElButton
              v-if="can('reviews:decide')" size="small" type="danger"
              @click="openReasonDialog(scope.row, 'take_down')"
            >下架</ElButton>
            <ElButton
              v-if="can('reviews:decide')" size="small" plain
              @click="decide(scope.row, 'limit')"
            >限流</ElButton>
          </div>
        </template>
      </ElTableColumn>
    </ElTable>

    <!-- 窄屏卡片：勾选框接管表格的多选，批量通过按钮继续可用。 -->
    <div v-if="!error && items.length" class="admin-cards">
      <article v-for="row in items" :key="row.content_id" class="admin-card">
        <header class="admin-card-head">
          <div class="card-lead">
            <ElCheckbox
              :model-value="selected.some((item) => item.content_id === row.content_id)"
              @change="onCardCheck(row, $event)"
            />
            <div>
              <div class="admin-card-title">
                <a :href="detailLink(row)" target="_blank" rel="noopener">
                  {{ row.title || '（无标题）' }}
                </a>
              </div>
              <div class="admin-card-sub">
                @{{ row.handle }} · {{ row.type === 'image' ? '图集' : '视频' }}
              </div>
            </div>
          </div>
          <div class="admin-card-tags">
            <AdminStatusTag
              :text="statusText[row.review_status] || row.review_status"
              :tone="reviewTone[row.review_status]"
            />
          </div>
        </header>
        <dl class="admin-card-meta">
          <div><dt>等待</dt><dd>{{ waitLabel(row.waiting_minutes) }}</dd></div>
          <div><dt>风险分</dt><dd>{{ row.risk_score }}</dd></div>
          <div>
            <dt>风险标签</dt>
            <dd>{{ row.risk_labels?.length ? row.risk_labels.join('、') : '—' }}</dd>
          </div>
        </dl>
        <div class="admin-card-actions">
          <ElButton
            v-if="can('reviews:decide')" size="small" type="primary" plain
            :loading="busyId === row.content_id" @click="decide(row, 'approve')"
          >通过</ElButton>
          <ElButton
            v-if="can('reviews:decide')" size="small" type="danger" plain
            @click="openReasonDialog(row, 'reject')"
          >驳回</ElButton>
          <ElButton
            v-if="can('reviews:decide')" size="small" type="danger"
            @click="openReasonDialog(row, 'take_down')"
          >下架</ElButton>
          <ElButton
            v-if="can('reviews:decide')" size="small" plain
            @click="decide(row, 'limit')"
          >限流</ElButton>
        </div>
      </article>
    </div>

    <ElDialog v-model="dialog.open" :title="dialog.action === 'reject' ? '驳回作品' : '下架作品'" width="460px">
      <p class="dialog-tip">
        理由会写入审计日志并通知作者；{{ dialog.action === 'take_down' ? '下架后立即退出 feed 与落地页。' : '作者可在收到通知后申诉。' }}
      </p>
      <ElInput
        v-model="dialog.reason" type="textarea" :rows="4" maxlength="200" show-word-limit
        placeholder="请填写具体理由（必填）"
      />
      <p v-if="dialog.error" class="dialog-error">{{ dialog.error }}</p>
      <template #footer>
        <ElButton @click="dialog.open = false">取消</ElButton>
        <ElButton type="danger" :loading="dialog.pending" @click="confirmReason">确认提交</ElButton>
      </template>
    </ElDialog>
    </div>
  </section>
</template>

<style scoped>
.review { display: grid; gap: 14px; }
.queue { display: grid; gap: 14px; }
.subtabs { display: flex; gap: 6px; }
.subtabs button {
  padding: 6px 14px; border-radius: 99px; cursor: pointer;
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: transparent; color: #a1a1aa; font-size: 12px;
}
.subtabs button.on {
  border-color: rgba(236, 72, 153, 0.6);
  background: rgba(236, 72, 153, 0.14); color: #fafafa;
}
.toolbar {
  display: flex; align-items: flex-start; justify-content: flex-end; gap: 16px;
}
.actions { display: flex; gap: 8px; }
.state {
  display: grid; gap: 12px; place-items: start; padding: 24px;
  border-radius: 16px; background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07); font-size: 13px; color: #a1a1aa;
}
.state p { margin: 0; }
.review-table { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; }
:deep(.high-risk) { background: rgba(255, 46, 126, 0.08) !important; }
.card-lead { display: flex; align-items: flex-start; gap: 8px; min-width: 0; }
.card-lead :deep(.el-checkbox) { margin-right: 0; flex: none; }
.cell { display: flex; gap: 10px; align-items: center; }
.cell img, .thumb {
  width: 56px; height: 56px; border-radius: 10px; object-fit: cover;
  background: rgba(255, 255, 255, 0.06);
}
.thumb { display: grid; place-items: center; font-size: 11px; color: #a1a1aa; }
.cell-body { display: grid; gap: 2px; min-width: 0; }
.title {
  color: #f4f4f5; font-size: 13px; font-weight: 600; text-decoration: none;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.title:hover { color: #ff8ab5; }
.sub { font-size: 11px; color: #8b8b93; }
.tags { display: flex; flex-wrap: wrap; gap: 4px; align-items: center; }
.score { font-size: 11px; color: #71717a; }
.row-actions { display: flex; flex-wrap: wrap; gap: 4px; }
.dialog-tip { margin: 0 0 12px; font-size: 12px; line-height: 1.7; color: #a1a1aa; }
.dialog-error { margin: 8px 0 0; font-size: 12px; color: #fb7185; }
</style>
