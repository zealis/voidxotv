<script setup lang="ts">
import {
  ElButton, ElDialog, ElEmpty, ElInput, ElMessage, ElTable, ElTableColumn, ElTag,
} from 'element-plus'
import type { AdminUserRow } from '~/composables/useAdminApi'

const { request } = useAdminApi()
const loading = ref(true)
const error = ref('')
const keyword = ref('')
const items = ref<AdminUserRow[]>([])

const dialog = reactive({
  open: false,
  action: 'warn' as 'warn' | 'mute' | 'ban' | 'unban' | 'verify_creator' | 'unverify_creator',
  user: null as AdminUserRow | null,
  reason: '',
  pending: false,
  error: '',
})

const actionLabels: Record<string, string> = {
  warn: '警告', mute: '禁言', ban: '封禁', unban: '解封',
  verify_creator: '创作者认证', unverify_creator: '取消认证',
}

// 从 6 张随机 DiceBear 头像里给指定用户挑一张（复用前台同一个候选组件与后端写入逻辑）。
const avatarDialog = reactive({
  open: false,
  user: null as AdminUserRow | null,
  seed: '',
  pending: false,
  error: '',
})

function openAvatarDialog(raw: unknown) {
  avatarDialog.open = true
  avatarDialog.user = raw as AdminUserRow
  avatarDialog.seed = ''
  avatarDialog.error = ''
}

async function applyAvatar() {
  if (!avatarDialog.user || !avatarDialog.seed) return
  avatarDialog.pending = true
  avatarDialog.error = ''
  try {
    await request(`/api/v1/admin/users/${avatarDialog.user.id}/avatar`, {
      method: 'POST', body: { seed: avatarDialog.seed }, csrf: true,
    })
    ElMessage.success(`已更新 @${avatarDialog.user.handle} 的头像`)
    avatarDialog.open = false
    await load()
  } catch (cause) {
    avatarDialog.error = cause instanceof Error ? cause.message : '操作失败'
  } finally {
    avatarDialog.pending = false
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const query = keyword.value.trim()
    const data = await request<{ items: AdminUserRow[] }>(
      `/api/v1/admin/users?limit=50${query ? `&q=${encodeURIComponent(query)}` : ''}`,
    )
    items.value = data.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

// Element Plus 表格插槽给的 scope.row 是宽松的 DefaultRow，传给子组件前收窄成业务类型。
function asUser(raw: unknown): AdminUserRow {
  return raw as AdminUserRow
}

function openDialog(raw: unknown, action: typeof dialog.action) {
  const user = raw as AdminUserRow
  dialog.open = true
  dialog.action = action
  dialog.user = user
  dialog.reason = ''
  dialog.error = ''
}

async function confirm() {
  if (!dialog.user) return
  if (!dialog.reason.trim()) {
    dialog.error = '处置理由必填，理由会通知用户'
    return
  }
  dialog.pending = true
  try {
    await request(`/api/v1/admin/users/${dialog.user.id}/sanction`, {
      method: 'POST',
      body: { action: dialog.action, reason: dialog.reason.trim() },
      csrf: true,
    })
    ElMessage.success(`已${actionLabels[dialog.action]} @${dialog.user.handle}`)
    dialog.open = false
    await load()
  } catch (cause) {
    dialog.error = cause instanceof Error ? cause.message : '操作失败'
  } finally {
    dialog.pending = false
  }
}

type Tone = 'positive' | 'attention' | 'danger' | 'neutral'
const statusMeta: Record<string, { text: string; tone: Tone }> = {
  active: { text: '正常', tone: 'positive' },
  muted: { text: '禁言', tone: 'attention' },
  banned: { text: '封禁', tone: 'danger' },
}
</script>

<template>
  <section class="users">
    <header class="toolbar">
      <div class="search">
        <ElInput
          v-model="keyword" placeholder="按账号搜索" clearable
          @keyup.enter="load" @clear="load"
        />
        <ElButton type="primary" @click="load">搜索</ElButton>
      </div>
    </header>

    <div v-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>
    <ElEmpty v-else-if="!loading && !items.length" description="没有匹配的用户" :image-size="96" />

    <template v-else>
      <ElTable v-loading="loading" :data="items" size="default" class="admin-table user-table">
        <ElTableColumn label="账号" min-width="140">
          <template #default="scope">
            <div class="handle">@{{ scope.row.handle }}</div>
            <div class="sub">ID {{ scope.row.id }}</div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="角色" width="84">
          <template #default="scope">
            <AdminStatusTag
              :text="scope.row.role === 'creator' ? '创作者' : '用户'"
              :tone="scope.row.role === 'creator' ? 'positive' : 'neutral'"
            />
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="80">
          <template #default="scope">
            <AdminStatusTag
              :text="statusMeta[scope.row.status]?.text || scope.row.status"
              :tone="statusMeta[scope.row.status]?.tone"
            />
          </template>
        </ElTableColumn>
        <ElTableColumn prop="content_count" label="作品" width="56" />
        <ElTableColumn label="最近处置" width="100">
          <template #default="scope">
            {{ scope.row.last_sanction ? (actionLabels[scope.row.last_sanction] || scope.row.last_sanction) : '—' }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="注册时间" width="112">
          <template #default="scope">{{ new Date(scope.row.created_at).toLocaleDateString('zh-CN') }}</template>
        </ElTableColumn>
        <ElTableColumn label="操作" min-width="296" fixed="right">
          <template #default="scope">
            <AdminUserActions
              :row="asUser(scope.row)" @sanction="openDialog" @avatar="openAvatarDialog"
            />
          </template>
        </ElTableColumn>
    </ElTable>

    <!-- 窄屏走卡片：表格有 7 列 + 右侧固定列，挤到 390px 会互相压字。
         样式在 assets/css/admin-cards.css，全后台共用一套断点与外壳。 -->
    <div v-loading="loading" class="admin-cards">
      <article v-for="row in items" :key="row.id" class="admin-card">
        <header class="admin-card-head">
          <div>
            <div class="admin-card-title">@{{ row.handle }}</div>
            <div class="admin-card-sub">ID {{ row.id }}</div>
          </div>
          <div class="admin-card-tags">
            <AdminStatusTag
              :text="row.role === 'creator' ? '创作者' : '用户'"
              :tone="row.role === 'creator' ? 'positive' : 'neutral'"
            />
            <AdminStatusTag
              :text="statusMeta[row.status]?.text || row.status"
              :tone="statusMeta[row.status]?.tone"
            />
          </div>
        </header>
        <dl class="admin-card-meta">
          <div><dt>作品</dt><dd>{{ row.content_count }}</dd></div>
          <div>
            <dt>最近处置</dt>
            <dd>{{ row.last_sanction ? (actionLabels[row.last_sanction] || row.last_sanction) : '—' }}</dd>
          </div>
          <div><dt>注册</dt><dd>{{ new Date(row.created_at).toLocaleDateString('zh-CN') }}</dd></div>
        </dl>
        <AdminUserActions :row="row" @sanction="openDialog" @avatar="openAvatarDialog" />
      </article>
    </div>
    </template>

    <ElDialog
      v-model="avatarDialog.open"
      :title="`设置 @${avatarDialog.user?.handle || ''} 的头像`"
      width="400px"
    >
      <AvatarPicker :selected="avatarDialog.seed" @select="avatarDialog.seed = $event" />
      <p v-if="avatarDialog.error" class="dialog-error">{{ avatarDialog.error }}</p>
      <template #footer>
        <ElButton @click="avatarDialog.open = false">取消</ElButton>
        <ElButton
          type="primary" :loading="avatarDialog.pending"
          :disabled="!avatarDialog.seed" @click="applyAvatar"
        >使用这张</ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="dialog.open" :title="`${actionLabels[dialog.action]} @${dialog.user?.handle || ''}`" width="440px">
      <p class="dialog-tip">处置动作写入审计日志，并同时向用户发送站内通知。</p>
      <ElInput
        v-model="dialog.reason" type="textarea" :rows="4" maxlength="200" show-word-limit
        placeholder="请填写处置理由（必填）"
      />
      <p v-if="dialog.error" class="dialog-error">{{ dialog.error }}</p>
      <template #footer>
        <ElButton @click="dialog.open = false">取消</ElButton>
        <ElButton type="danger" :loading="dialog.pending" @click="confirm">确认{{ actionLabels[dialog.action] }}</ElButton>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.users { display: grid; gap: 14px; }
.toolbar { display: flex; align-items: flex-start; justify-content: flex-end; gap: 16px; flex-wrap: wrap; }
.search { display: flex; gap: 8px; width: min(340px, 100%); }
.state {
  display: grid; gap: 12px; place-items: start; padding: 24px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.07);
  font-size: 13px; color: #a1a1aa;
}
.state p { margin: 0; }
.user-table { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; }
.handle { font-size: 13px; font-weight: 600; color: #f4f4f5; }
.sub { font-size: 11px; color: #8b8b93; }
/* 卡片外壳与 ≤1160px 断点统一在 assets/css/admin-cards.css，
   这里只留表格 / 弹窗专用样式。 */
.dialog-tip { margin: 0 0 12px; font-size: 12px; line-height: 1.7; color: #a1a1aa; }
.dialog-error { margin: 8px 0 0; font-size: 12px; color: #fb7185; }

/* 表格 7 列最小宽度合计约 868px：加上侧栏 220 与主区内边距，窗口窄到这个尺寸以下
   就会横向滚动，右侧固定列随即压住「最近处置 / 注册时间」。1160px 的切换规则
   见 admin-cards.css 的 .admin-table / .admin-cards。 */
</style>
