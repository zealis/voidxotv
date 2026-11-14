<script setup lang="ts">
import {
  ElButton, ElDialog, ElInput, ElMessage, ElMessageBox, ElOption, ElSelect,
  ElTable, ElTableColumn,
} from 'element-plus'

// 账号与权限（§4.7）：只有超管可见。改角色 / 停用 / 重置口令都会吊销目标会话，
// 目标必须重新登录，权限才按新角色生效。
interface AccountRow {
  id: number
  email: string
  role: string
  status: string
  created_at: string
  updated_at: string
}

const { request, can, session } = useAdminApi()

const loading = ref(true)
const error = ref('')
const items = ref<AccountRow[]>([])
const roles = ref<string[]>(['super_admin', 'reviewer', 'operator', 'readonly'])
const saving = ref(0)

const roleLabels: Record<string, string> = {
  super_admin: '超级管理员', reviewer: '审核员', operator: '运营', readonly: '只读',
}
const roleHints: Record<string, string> = {
  super_admin: '全部权限，含账号管理',
  reviewer: '审核、举报处置、只读查询',
  operator: '运营配置、用户处置、交付运维',
  readonly: '只读，不能执行任何写操作',
}

// 后台登录标识与前台同一口径：只认邮箱（0.17.0 起取消用户名）。
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

const createDialog = reactive({
  open: false, email: '', role: 'reviewer', password: '', pending: false, error: '',
})
const resetDialog = reactive({
  open: false, target: null as AccountRow | null, password: '', pending: false, error: '',
})

const writable = computed(() => can('accounts:write'))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ items: AccountRow[]; roles?: string[] }>('/api/v1/admin/accounts')
    items.value = data.items || []
    if (data.roles?.length) roles.value = data.roles
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openCreate() {
  createDialog.open = true
  createDialog.email = ''
  createDialog.role = 'reviewer'
  createDialog.password = ''
  createDialog.error = ''
}

// 邮箱本身与 @ 前面的本地部分都不许当密码（服务端同规则）。
function validatePassword(password: string, email: string): string {
  if (password.length < 8) return '密码至少 8 位'
  if (password.length > 72) return '密码不能超过 72 字节'
  if (/\s/.test(password)) return '密码不能包含空白字符'
  const local = email.split('@')[0] || ''
  const lower = password.toLowerCase()
  if (lower === email.toLowerCase() || (local && lower === local.toLowerCase())) {
    return '密码不能与登录邮箱相同'
  }
  return ''
}

async function submitCreate() {
  const email = createDialog.email.trim().toLowerCase()
  if (!EMAIL_PATTERN.test(email)) {
    createDialog.error = '请输入有效的邮箱地址'
    return
  }
  const invalid = validatePassword(createDialog.password, email)
  if (invalid) {
    createDialog.error = invalid
    return
  }
  createDialog.pending = true
  createDialog.error = ''
  try {
    await request('/api/v1/admin/accounts', {
      method: 'POST',
      csrf: true,
      body: { email, role: createDialog.role, password: createDialog.password },
    })
    createDialog.open = false
    ElMessage.success(`管理员 ${email} 已创建`)
    await load()
  } catch (cause) {
    createDialog.error = cause instanceof Error ? cause.message : '创建失败'
  } finally {
    createDialog.pending = false
  }
}

async function changeRole(raw: unknown, next: string) {
  const row = raw as AccountRow
  const previous = row.role
  if (next === previous) return
  try {
    await ElMessageBox.confirm(
      `把 ${row.email} 的角色从「${roleLabels[previous]}」改为「${roleLabels[next]}」？` +
      '\n该账号的登录会立即失效，需要重新登录。',
      '调整角色',
      { type: 'warning', confirmButtonText: '确认调整', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  saving.value = row.id
  try {
    await request(`/api/v1/admin/accounts/${row.id}/role`, {
      method: 'POST', csrf: true, body: { role: next },
    })
    ElMessage.success('角色已更新，该账号需重新登录')
    await load()
  } catch (cause) {
    row.role = previous
    ElMessage.error(cause instanceof Error ? cause.message : '调整失败')
  } finally {
    saving.value = 0
  }
}

async function toggleStatus(raw: unknown) {
  const row = raw as AccountRow
  const next = row.status === 'active' ? 'disabled' : 'active'
  try {
    await ElMessageBox.confirm(
      next === 'disabled'
        ? `停用 ${row.email}？该账号会立即退出登录且无法登录。`
        : `启用 ${row.email}？启用后该账号可以正常登录。`,
      next === 'disabled' ? '停用账号' : '启用账号',
      { type: 'warning', confirmButtonText: '确认', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  saving.value = row.id
  try {
    await request(`/api/v1/admin/accounts/${row.id}/status`, {
      method: 'POST', csrf: true, body: { status: next },
    })
    ElMessage.success(next === 'disabled' ? '账号已停用' : '账号已启用')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '操作失败')
  } finally {
    saving.value = 0
  }
}

function openReset(raw: unknown) {
  const row = raw as AccountRow
  resetDialog.open = true
  resetDialog.target = row
  resetDialog.password = ''
  resetDialog.error = ''
}

async function submitReset() {
  const target = resetDialog.target
  if (!target) return
  const invalid = validatePassword(resetDialog.password, target.email)
  if (invalid) {
    resetDialog.error = invalid
    return
  }
  resetDialog.pending = true
  resetDialog.error = ''
  try {
    await request(`/api/v1/admin/accounts/${target.id}/password`, {
      method: 'POST', csrf: true, body: { password: resetDialog.password },
    })
    resetDialog.open = false
    ElMessage.success(`${target.email} 的密码已重置，需要重新登录`)
    await load()
  } catch (cause) {
    resetDialog.error = cause instanceof Error ? cause.message : '重置失败'
  } finally {
    resetDialog.pending = false
  }
}

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const isSelf = (raw: unknown) => (raw as AccountRow).id === session.value?.id
</script>

<template>
  <section class="accounts">
    <header class="head">
      <div>
        <h3>后台账号</h3>
        <p>账号用邮箱登录，与前台用户体系分离；权限只在服务端判定，前端按钮显隐只是跟随。</p>
      </div>
      <ElButton v-if="writable" type="primary" @click="openCreate">新建账号</ElButton>
    </header>

    <p v-if="error" class="error">{{ error }}</p>

    <ElTable v-loading="loading" :data="items" empty-text="暂无账号" size="small" row-key="id">
      <ElTableColumn prop="email" label="邮箱" min-width="220">
        <template #default="{ row }">
          <span class="name">{{ row.email }}</span>
          <AdminStatusTag v-if="isSelf(row)" class="self-tag" text="当前登录" tone="neutral" />
        </template>
      </ElTableColumn>

      <ElTableColumn label="角色" width="220">
        <template #default="{ row }">
          <ElSelect
            v-if="writable && !isSelf(row)"
            :model-value="row.role"
            size="small"
            :disabled="saving === row.id"
            @change="(value: string) => changeRole(row, value)"
          >
            <ElOption v-for="role in roles" :key="role" :value="role" :label="roleLabels[role]">
              <span class="opt">{{ roleLabels[role] }}</span>
              <em class="opt-hint">{{ roleHints[role] }}</em>
            </ElOption>
          </ElSelect>
          <span v-else>{{ roleLabels[row.role] || row.role }}</span>
        </template>
      </ElTableColumn>

      <ElTableColumn label="状态" width="110">
        <template #default="{ row }">
          <AdminStatusTag
            :text="row.status === 'active' ? '启用' : '停用'"
            :tone="row.status === 'active' ? 'positive' : 'neutral'"
          />
        </template>
      </ElTableColumn>

      <ElTableColumn label="创建时间" width="150">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </ElTableColumn>

      <ElTableColumn label="操作" min-width="170" align="right">
        <template #default="{ row }">
          <ElButton v-if="writable" link type="primary" size="small" @click="openReset(row)">
            重置密码
          </ElButton>
          <ElButton
            v-if="writable && !isSelf(row)"
            link :type="row.status === 'active' ? 'danger' : 'success'" size="small"
            :loading="saving === row.id" @click="toggleStatus(row)"
          >
            {{ row.status === 'active' ? '停用' : '启用' }}
          </ElButton>
        </template>
      </ElTableColumn>
    </ElTable>

    <p class="note">
      改角色 / 停用 / 重置密码会立即吊销该账号的全部会话；不能停用或降级自己，
      也不能让系统失去最后一个可用的超级管理员。
    </p>

    <ElDialog v-model="createDialog.open" title="新建后台账号" width="420px">
      <div class="form">
        <label>登录邮箱</label>
        <ElInput
          v-model="createDialog.email" type="email" placeholder="如 editor@nightstreet.cn"
          maxlength="254"
        />
        <label>角色</label>
        <ElSelect v-model="createDialog.role" class="full">
          <ElOption v-for="role in roles" :key="role" :value="role" :label="roleLabels[role]" />
        </ElSelect>
        <p class="hint">{{ roleHints[createDialog.role] }}</p>
        <label>初始密码</label>
        <ElInput
          v-model="createDialog.password" type="password" show-password autocomplete="new-password"
          placeholder="至少 8 位，交付后请对方自行修改"
        />
      </div>
      <p v-if="createDialog.error" class="error">{{ createDialog.error }}</p>
      <template #footer>
        <ElButton @click="createDialog.open = false">取消</ElButton>
        <ElButton type="primary" :loading="createDialog.pending" @click="submitCreate">创建</ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="resetDialog.open" title="重置密码" width="420px">
      <p class="hint">
        为 <strong>{{ resetDialog.target?.email }}</strong> 设置新密码，
        该账号的所有登录会立即失效。
      </p>
      <ElInput
        v-model="resetDialog.password" type="password" show-password autocomplete="new-password"
        placeholder="至少 8 位"
      />
      <p v-if="resetDialog.error" class="error">{{ resetDialog.error }}</p>
      <template #footer>
        <ElButton @click="resetDialog.open = false">取消</ElButton>
        <ElButton type="primary" :loading="resetDialog.pending" @click="submitReset">确认重置</ElButton>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.accounts { display: grid; gap: 14px; }
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.head h3 { margin: 0 0 4px; font-size: 15px; }
.head p { margin: 0; font-size: 12px; color: #8b8b93; }
.name { font-weight: 600; }
.self-tag { margin-left: 8px; }
.opt { display: inline-block; min-width: 72px; }
.opt-hint { font-style: normal; font-size: 11px; color: #8b8b93; }
.note { margin: 0; font-size: 12px; line-height: 1.6; color: #8b8b93; }
.error { margin: 0; font-size: 12px; color: #ff6961; }
.form { display: grid; gap: 8px; }
.form label { font-size: 12px; color: #a1a1aa; }
.full { width: 100%; }
.hint { margin: 0; font-size: 12px; line-height: 1.6; color: #8b8b93; }
</style>
