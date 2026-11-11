<script setup lang="ts">
import {
  ElButton, ElDialog, ElEmpty, ElInput, ElMessage, ElTable, ElTableColumn,
} from 'element-plus'

// 作者申诉队列（§9.4）：通过后内容恢复为 approved 并走同一 Outbox 失效链路。
interface AppealRow {
  id: number
  content_id: number
  slug: string
  title: string
  handle: string
  review_status: string
  decision_action: string
  decision_reason: string
  reason: string
  status: string
  created_at: string
}

const emit = defineEmits<{ changed: [] }>()
const { request, can } = useAdminApi()

const loading = ref(true)
const error = ref('')
const items = ref<AppealRow[]>([])
const busyId = ref<number | null>(null)
const dialog = reactive({
  open: false,
  approve: true,
  item: null as AppealRow | null,
  note: '',
  pending: false,
  error: '',
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ items: AppealRow[] }>('/api/v1/admin/appeals?status=pending')
    items.value = data.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

function detailLink(raw: unknown): string {
  const item = raw as AppealRow
  return `/video/${item.slug}`
}

function openDialog(raw: unknown, approve: boolean) {
  const item = raw as AppealRow
  dialog.open = true
  dialog.approve = approve
  dialog.item = item
  dialog.note = ''
  dialog.error = ''
}

async function confirm() {
  if (!dialog.item) return
  if (!dialog.approve && !dialog.note.trim()) {
    dialog.error = '驳回申诉必须填写说明，说明会通知作者'
    return
  }
  dialog.pending = true
  dialog.error = ''
  try {
    await request(`/api/v1/admin/appeals/${dialog.item.id}/decision`, {
      method: 'POST',
      body: { approve: dialog.approve, note: dialog.note },
      csrf: true,
    })
    ElMessage.success(dialog.approve ? '申诉已通过，作品已恢复' : '申诉已驳回')
    dialog.open = false
    await load()
    emit('changed')
  } catch (cause) {
    dialog.error = cause instanceof Error ? cause.message : '操作失败'
  } finally {
    dialog.pending = false
  }
}

const reviewText: Record<string, string> = {
  rejected: '已驳回', taken_down: '已下架', limited: '已限流',
}
</script>

<template>
  <section class="appeals">
    <header class="toolbar">
      <div class="actions">
        <ElButton @click="load">刷新</ElButton>
      </div>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>
    <ElEmpty v-else-if="!loading && !items.length" description="暂无待处理申诉" :image-size="96" />

    <ElTable v-else v-loading="loading" :data="items" size="default">
      <ElTableColumn label="作品" min-width="220">
        <template #default="{ row }">
          <a :href="detailLink(row)" target="_blank" rel="noopener" class="title">
            {{ row.title || '（无标题）' }}
          </a>
          <span class="sub">@{{ row.handle }} · {{ row.slug }}</span>
        </template>
      </ElTableColumn>
      <ElTableColumn label="原处置" min-width="200">
        <template #default="{ row }">
          <AdminStatusTag :text="reviewText[row.review_status] || row.review_status" tone="danger" />
          <p class="reason">{{ row.decision_reason || '（未填写理由）' }}</p>
        </template>
      </ElTableColumn>
      <ElTableColumn label="申诉理由" min-width="240">
        <template #default="{ row }">
          <p class="reason">{{ row.reason }}</p>
        </template>
      </ElTableColumn>
      <ElTableColumn label="操作" width="190" fixed="right">
        <template #default="{ row }">
          <div class="row-actions">
            <ElButton
              v-if="can('reviews:decide')" size="small" type="primary"
              :loading="busyId === row.id" @click="openDialog(row, true)"
            >通过</ElButton>
            <ElButton
              v-if="can('reviews:decide')" size="small" type="danger" plain
              :disabled="busyId === row.id" @click="openDialog(row, false)"
            >驳回</ElButton>
            <span v-if="!can('reviews:decide')" class="readonly">只读</span>
          </div>
        </template>
      </ElTableColumn>
    </ElTable>

    <ElDialog v-model="dialog.open" :title="dialog.approve ? '通过申诉' : '驳回申诉'" width="460px">
      <p class="dialog-tip">
        {{ dialog.approve
          ? '通过后作品恢复为已通过并重新进入公开检索，同时触发页面再验证与缓存失效。'
          : '驳回需说明理由，作者会在站内通知中看到结论。' }}
      </p>
      <ElInput
        v-model="dialog.note" type="textarea" :rows="4"
        :placeholder="dialog.approve ? '可选：给作者的说明' : '必填：驳回理由'"
      />
      <p v-if="dialog.error" class="dialog-error">{{ dialog.error }}</p>
      <template #footer>
        <ElButton @click="dialog.open = false">取消</ElButton>
        <ElButton :type="dialog.approve ? 'primary' : 'danger'" :loading="dialog.pending" @click="confirm">
          确认
        </ElButton>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.appeals { display: grid; gap: 14px; }
.toolbar { display: flex; justify-content: flex-end; }
.title { color: #fafafa; font-size: 13px; text-decoration: none; }
.title:hover { text-decoration: underline; }
.sub { display: block; margin-top: 2px; font-size: 11px; color: #71717a; }
.reason { margin: 4px 0 0; font-size: 12px; line-height: 1.6; color: #a1a1aa; }
.row-actions { display: flex; gap: 6px; align-items: center; }
.readonly { font-size: 11px; color: #71717a; }
.dialog-tip { margin: 0 0 12px; font-size: 12px; line-height: 1.7; color: #a1a1aa; }
.dialog-error { margin: 8px 0 0; font-size: 12px; color: #fb7185; }
</style>
