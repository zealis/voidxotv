<script setup lang="ts">
import { ElButton, ElDialog, ElInput, ElMessage } from 'element-plus'

// 自助改密：任何已登录账号都能改自己的口令。改完只保留当前会话，其它设备全部下线。
const { request, session } = useAdminApi()

const open = ref(false)
const form = reactive({
  current: '', next: '', confirm: '', pending: false, error: '',
})

function reset() {
  form.current = ''
  form.next = ''
  form.confirm = ''
  form.error = ''
}

function openDialog() {
  reset()
  open.value = true
}

async function submit() {
  if (!form.current) {
    form.error = '请填写当前密码'
    return
  }
  if (form.next.length < 8) {
    form.error = '新密码至少 8 位'
    return
  }
  if (/\s/.test(form.next)) {
    form.error = '新密码不能包含空白字符'
    return
  }
  if (form.next !== form.confirm) {
    form.error = '两次输入的新密码不一致'
    return
  }
  if (form.next === form.current) {
    form.error = '新密码不能与当前密码相同'
    return
  }
  form.pending = true
  form.error = ''
  try {
    const result = await request<{ revoked_sessions: number }>('/api/v1/admin/auth/password', {
      method: 'POST', csrf: true,
      body: { current_password: form.current, new_password: form.next },
    })
    open.value = false
    ElMessage.success(result.revoked_sessions
      ? `密码已更新，另外 ${result.revoked_sessions} 个会话已下线`
      : '密码已更新')
  } catch (cause) {
    form.error = cause instanceof Error ? cause.message : '修改失败'
  } finally {
    form.pending = false
  }
}
</script>

<template>
  <ElButton size="small" @click="openDialog">修改密码</ElButton>

  <ElDialog v-model="open" title="修改我的密码" width="420px" @closed="reset">
    <p class="hint">
      当前账号：<strong>{{ session?.email }}</strong>。修改成功后其它设备会立即退出，
      当前会话保留。
    </p>
    <div class="form">
      <label>当前密码</label>
      <ElInput v-model="form.current" type="password" show-password autocomplete="current-password" />
      <label>新密码</label>
      <ElInput v-model="form.next" type="password" show-password autocomplete="new-password" placeholder="至少 8 位" />
      <label>确认新密码</label>
      <ElInput v-model="form.confirm" type="password" show-password autocomplete="new-password" />
    </div>
    <p v-if="form.error" class="error">{{ form.error }}</p>
    <template #footer>
      <ElButton @click="open = false">取消</ElButton>
      <ElButton type="primary" :loading="form.pending" @click="submit">确认修改</ElButton>
    </template>
  </ElDialog>
</template>

<style scoped>
.hint { margin: 0 0 12px; font-size: 12px; line-height: 1.6; color: #8b8b93; }
.form { display: grid; gap: 8px; }
.form label { font-size: 12px; color: #a1a1aa; }
.error { margin: 10px 0 0; font-size: 12px; color: #ff6961; }
</style>
