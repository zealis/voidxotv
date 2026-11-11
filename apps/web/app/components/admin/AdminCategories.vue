<script setup lang="ts">
import {
  ElButton, ElDialog, ElEmpty, ElInput, ElInputNumber, ElMessage, ElTable, ElTableColumn,
} from 'element-plus'

// 分类配置（§4.7 归入系统设置）：分类页 URL 与内容归集的基础数据。
interface CategoryRow {
  id: number
  slug: string
  name: string
  description: string
  position: number
  status: string
  content_count: number
  created_at: string
}

const { request, can } = useAdminApi()

const loading = ref(true)
const error = ref('')
const items = ref<CategoryRow[]>([])
const dialog = reactive({
  open: false,
  item: null as CategoryRow | null,
  slug: '',
  name: '',
  description: '',
  position: 100,
  pending: false,
  error: '',
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ items: CategoryRow[] }>('/api/v1/admin/categories')
    items.value = data.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openCreate() {
  dialog.open = true
  dialog.item = null
  dialog.slug = ''
  dialog.name = ''
  dialog.description = ''
  dialog.position = (items.value.reduce((max, item) => Math.max(max, item.position), 0) || 0) + 10
  dialog.error = ''
}

function openEdit(raw: unknown) {
  const item = raw as CategoryRow
  dialog.open = true
  dialog.item = item
  dialog.slug = item.slug
  dialog.name = item.name
  dialog.description = item.description
  dialog.position = item.position
  dialog.error = ''
}

async function save() {
  if (!dialog.name.trim()) {
    dialog.error = '分类名称必填'
    return
  }
  if (!dialog.item && !dialog.slug.trim()) {
    dialog.error = '新建分类必须填写 slug'
    return
  }
  dialog.pending = true
  dialog.error = ''
  try {
    await request('/api/v1/admin/categories', {
      method: 'POST',
      body: {
        id: dialog.item?.id || 0,
        slug: dialog.slug,
        name: dialog.name,
        description: dialog.description,
        position: dialog.position,
        status: dialog.item?.status || 'available',
      },
      csrf: true,
    })
    ElMessage.success(dialog.item ? '分类已更新' : '分类已创建')
    dialog.open = false
    await load()
  } catch (cause) {
    dialog.error = cause instanceof Error ? cause.message : '保存失败'
  } finally {
    dialog.pending = false
  }
}

async function toggle(raw: unknown) {
  const item = raw as CategoryRow
  const next = item.status === 'available' ? 'hidden' : 'available'
  try {
    await request(`/api/v1/admin/categories/${item.id}/status`, {
      method: 'POST', body: { status: next }, csrf: true,
    })
    ElMessage.success(next === 'available' ? '分类已上线' : '分类已隐藏')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '操作失败')
  }
}
</script>

<template>
  <section class="categories">
    <header class="head">
      <div>
        <h3>分类配置</h3>
        <p>分类是搜索筛选与 /category/[slug] 的数据源；隐藏不会删除已归类内容。</p>
      </div>
      <ElButton v-if="can('settings:write')" type="primary" @click="openCreate">新建分类</ElButton>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>
    <ElEmpty v-else-if="!loading && !items.length" description="暂无分类" :image-size="72" />

    <ElTable v-else v-loading="loading" :data="items" size="small">
      <ElTableColumn label="名称" min-width="140">
        <template #default="{ row }">
          <strong class="name">{{ row.name }}</strong>
          <span class="slug">{{ row.slug }}</span>
        </template>
      </ElTableColumn>
      <ElTableColumn prop="description" label="说明" min-width="180" show-overflow-tooltip />
      <ElTableColumn prop="position" label="排序" width="70" />
      <ElTableColumn label="状态" width="90">
        <template #default="{ row }">
          <AdminStatusTag
            :text="row.status === 'available' ? '已上线' : '已隐藏'"
            :tone="row.status === 'available' ? 'positive' : 'neutral'"
          />
        </template>
      </ElTableColumn>
      <ElTableColumn prop="content_count" label="内容数" width="80" />
      <ElTableColumn label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <template v-if="can('settings:write')">
            <ElButton size="small" link type="primary" @click="openEdit(row)">编辑</ElButton>
            <ElButton size="small" link :type="row.status === 'available' ? 'warning' : 'success'" @click="toggle(row)">
              {{ row.status === 'available' ? '隐藏' : '上线' }}
            </ElButton>
          </template>
          <span v-else class="readonly">只读</span>
        </template>
      </ElTableColumn>
    </ElTable>

    <ElDialog v-model="dialog.open" :title="dialog.item ? '编辑分类' : '新建分类'" width="440px">
      <div class="form">
        <label>
          <span>名称</span>
          <ElInput v-model="dialog.name" maxlength="20" placeholder="如：夜景" />
        </label>
        <label>
          <span>slug</span>
          <ElInput
            v-model="dialog.slug" :disabled="!!dialog.item" maxlength="40"
            placeholder="night-city（小写字母/数字/连字符）"
          />
        </label>
        <label>
          <span>说明</span>
          <ElInput v-model="dialog.description" maxlength="120" placeholder="用于分类页与搜索筛选项" />
        </label>
        <label>
          <span>排序</span>
          <ElInputNumber v-model="dialog.position" :min="0" :max="9999" />
        </label>
      </div>
      <p v-if="dialog.error" class="dialog-error">{{ dialog.error }}</p>
      <template #footer>
        <ElButton @click="dialog.open = false">取消</ElButton>
        <ElButton type="primary" :loading="dialog.pending" @click="save">保存</ElButton>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.categories { display: grid; gap: 12px; margin-top: 18px; }
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.head h3 { margin: 0 0 4px; font-size: 14px; color: #fafafa; }
.head p { margin: 0; font-size: 12px; color: #8b8b93; }
.name { display: block; font-size: 13px; color: #fafafa; }
.slug { font-size: 11px; color: #71717a; }
.readonly { font-size: 11px; color: #71717a; }
.form { display: grid; gap: 12px; }
.form label { display: grid; gap: 6px; font-size: 12px; color: #a1a1aa; }
.dialog-error { margin: 8px 0 0; font-size: 12px; color: #fb7185; }
.state { padding: 12px 0; font-size: 13px; color: #a1a1aa; }
</style>
