<script setup lang="ts">
import {
  ElButton, ElDialog, ElEmpty, ElInput, ElMessage, ElOption, ElSelect,
  ElTable, ElTableColumn,
} from 'element-plus'
import type { AdminTopic } from '~/composables/useAdminApi'

const { request, can } = useAdminApi()
const loading = ref(true)
const error = ref('')
const items = ref<AdminTopic[]>([])

const createDialog = reactive({ open: false, slug: '', name: '', pending: false, error: '' })
const mergeDialog = reactive({
  open: false, source: null as AdminTopic | null, target: '', pending: false, error: '',
})

type Tone = 'positive' | 'attention' | 'danger' | 'neutral'
const statusMeta: Record<string, { text: string; tone: Tone }> = {
  available: { text: '可用', tone: 'positive' },
  pending: { text: '待审', tone: 'attention' },
  offline: { text: '已下线', tone: 'neutral' },
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ items: AdminTopic[] }>('/api/v1/admin/topics')
    items.value = data.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function setStatus(raw: unknown, status: string) {
  const topic = raw as AdminTopic
  try {
    await request(`/api/v1/admin/topics/${topic.id}/status`, {
      method: 'POST', body: { status }, csrf: true,
    })
    ElMessage.success(status === 'available' ? '话题已通过' : '话题已下线')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '操作失败')
  }
}

async function createTopic() {
  createDialog.error = ''
  if (!createDialog.slug.trim() || !createDialog.name.trim()) {
    createDialog.error = 'slug 与名称必填'
    return
  }
  createDialog.pending = true
  try {
    await request('/api/v1/admin/topics', {
      method: 'POST',
      body: { slug: createDialog.slug.trim(), name: createDialog.name.trim() },
      csrf: true,
    })
    ElMessage.success('话题已创建')
    createDialog.open = false
    createDialog.slug = ''
    createDialog.name = ''
    await load()
  } catch (cause) {
    createDialog.error = cause instanceof Error ? cause.message : '创建失败'
  } finally {
    createDialog.pending = false
  }
}

function openMerge(raw: unknown) {
  const topic = raw as AdminTopic
  mergeDialog.open = true
  mergeDialog.source = topic
  mergeDialog.target = ''
  mergeDialog.error = ''
}

async function merge() {
  const target = Number(mergeDialog.target)
  if (!mergeDialog.source || !target) {
    mergeDialog.error = '请输入目标话题 ID'
    return
  }
  mergeDialog.pending = true
  try {
    const result = await request<{ moved: number }>(
      `/api/v1/admin/topics/${mergeDialog.source.id}/merge`,
      { method: 'POST', body: { target_id: target }, csrf: true },
    )
    ElMessage.success(`已迁移 ${result.moved} 条作品关联`)
    mergeDialog.open = false
    await load()
  } catch (cause) {
    mergeDialog.error = cause instanceof Error ? cause.message : '合并失败'
  } finally {
    mergeDialog.pending = false
  }
}

async function setPlacement(raw: unknown, placement: string) {
  const topic = raw as AdminTopic
  try {
    await request('/api/v1/admin/topics/placements', {
      method: 'POST',
      body: { topic_id: topic.id, placement },
      csrf: true,
    })
    ElMessage.success('运营位已更新')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '操作失败')
  }
}
</script>

<template>
  <section class="topics">
    <header class="toolbar">
      <div class="actions">
        <ElButton v-if="can('topics:write')" type="primary" @click="createDialog.open = true">
          新建话题
        </ElButton>
        <ElButton @click="load">刷新</ElButton>
      </div>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>
    <ElEmpty v-else-if="!loading && !items.length" description="暂无话题" :image-size="96" />

    <ElTable v-else v-loading="loading" :data="items" size="default" class="topic-table">
      <ElTableColumn label="话题" min-width="200">
        <template #default="scope">
          <div class="name">#{{ scope.row.name }}</div>
          <div class="sub">{{ scope.row.slug }}</div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="状态" width="92">
        <template #default="scope">
          <AdminStatusTag
            :text="statusMeta[scope.row.status]?.text || scope.row.status"
            :tone="statusMeta[scope.row.status]?.tone"
          />
        </template>
      </ElTableColumn>
      <ElTableColumn prop="content_count" label="作品数" width="80" />
      <ElTableColumn label="运营位" width="96">
        <template #default="scope">
          <span class="sub">
            {{ scope.row.placement === 'featured' ? '推荐' : scope.row.placement === 'pinned' ? '置顶' : '—' }}
          </span>
        </template>
      </ElTableColumn>
      <ElTableColumn label="操作" min-width="300" fixed="right">
        <template #default="scope">
          <div v-if="can('topics:write')" class="row-actions">
            <ElButton
              v-if="scope.row.status === 'pending'" size="small" type="primary" plain
              @click="setStatus(scope.row, 'available')"
            >通过审核</ElButton>
            <ElButton
              v-if="scope.row.status === 'available'" size="small" plain
              @click="setStatus(scope.row, 'offline')"
            >下线</ElButton>
            <ElButton
              v-if="scope.row.status === 'offline'" size="small" plain
              @click="setStatus(scope.row, 'available')"
            >恢复</ElButton>
            <ElButton size="small" plain @click="setPlacement(scope.row, 'featured')">推荐</ElButton>
            <ElButton size="small" plain @click="setPlacement(scope.row, 'pinned')">置顶</ElButton>
            <ElButton size="small" plain type="danger" @click="openMerge(scope.row)">合并</ElButton>
          </div>
          <span v-else class="sub">只读角色</span>
        </template>
      </ElTableColumn>
    </ElTable>

    <ElDialog v-model="createDialog.open" title="新建话题" width="420px">
      <div class="dialog-body">
        <label>slug（小写字母、数字与连字符）</label>
        <ElInput v-model="createDialog.slug" placeholder="night-walk" maxlength="48" />
        <label>话题名称</label>
        <ElInput v-model="createDialog.name" placeholder="夜行" maxlength="30" />
        <p v-if="createDialog.error" class="dialog-error">{{ createDialog.error }}</p>
      </div>
      <template #footer>
        <ElButton @click="createDialog.open = false">取消</ElButton>
        <ElButton type="primary" :loading="createDialog.pending" @click="createTopic">创建</ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="mergeDialog.open" :title="`合并 #${mergeDialog.source?.name || ''}`" width="420px">
      <div class="dialog-body">
        <p class="dialog-tip">合并会把作品关联迁移到目标话题，并把当前话题置为已下线。</p>
        <label>目标话题 ID</label>
        <ElInput v-model="mergeDialog.target" placeholder="例如 1" />
        <p v-if="mergeDialog.error" class="dialog-error">{{ mergeDialog.error }}</p>
      </div>
      <template #footer>
        <ElButton @click="mergeDialog.open = false">取消</ElButton>
        <ElButton type="danger" :loading="mergeDialog.pending" @click="merge">确认合并</ElButton>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.topics { display: grid; gap: 14px; }
.toolbar { display: flex; align-items: flex-start; justify-content: flex-end; gap: 16px; }
.actions { display: flex; gap: 8px; }
.state {
  display: grid; gap: 12px; place-items: start; padding: 24px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.07);
  font-size: 13px; color: #a1a1aa;
}
.state p { margin: 0; }
.topic-table { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; }
.name { font-size: 13px; font-weight: 600; color: #f4f4f5; }
.sub { font-size: 11px; color: #8b8b93; }
.row-actions { display: flex; flex-wrap: wrap; gap: 4px; }
.dialog-body { display: grid; gap: 8px; }
.dialog-body label { font-size: 12px; color: #8b8b93; }
.dialog-tip { margin: 0 0 4px; font-size: 12px; line-height: 1.7; color: #a1a1aa; }
.dialog-error { margin: 4px 0 0; font-size: 12px; color: #fb7185; }
</style>
