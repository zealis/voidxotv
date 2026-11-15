<script setup lang="ts">
// 敏感操作的身份确认弹窗：前台与后台共用，按 state.scope 调对应接口。
// 后台开了 TOTP 的账号会先收到 totp_required，再显示验证码输入框。
import { ElButton, ElDialog, ElInput } from 'element-plus'

const { state, succeed, cancel } = useStepUp()
const { api } = useApi()

const password = ref('')
const totp = ref('')
const needTotp = ref(false)
const busy = ref(false)
const error = ref('')

watch(() => state.value.open, (open) => {
  if (!open) return
  password.value = ''
  totp.value = ''
  needTotp.value = false
  error.value = ''
})

function messageOf(cause: unknown): string {
  const payload = (cause as { data?: { error?: { code?: string } } }).data
  const code = payload?.error?.code || (cause as { code?: string }).code || ''
  if (code === 'totp_required') {
    needTotp.value = true
    return '请输入动态验证码'
  }
  if (code === 'invalid_totp') return '动态验证码不正确'
  if (code === 'invalid_credentials') return '密码不正确'
  return '确认失败，请稍后再试'
}

async function submit() {
  if (busy.value) return
  if (!password.value) {
    error.value = '请输入登录密码'
    return
  }
  busy.value = true
  error.value = ''
  const scope = state.value.scope
  try {
    if (scope === 'admin') {
      const { request } = useAdminApi()
      const data = await request<{ token: string; expires_in: number }>(
        '/api/v1/admin/auth/step-up',
        {
          method: 'POST', csrf: true,
          body: { password: password.value, totp_code: totp.value || undefined },
        },
      )
      succeed(scope, data.token, data.expires_in)
    } else {
      const data = await api<{ token: string; expires_in: number }>(
        '/api/v1/auth/step-up',
        { method: 'POST', body: { password: password.value } },
      )
      succeed(scope, data.token, data.expires_in)
    }
  } catch (cause) {
    error.value = messageOf(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ElDialog
    :model-value="state.open" title="确认身份" width="400px"
    :close-on-click-modal="false" @close="cancel"
  >
    <p class="tip">{{ state.reason }}</p>

    <label class="field-label">登录密码</label>
    <ElInput
      v-model="password" type="password" show-password autocomplete="current-password"
      placeholder="请输入当前登录密码" @keyup.enter="submit"
    />

    <template v-if="needTotp">
      <label class="field-label">动态验证码</label>
      <ElInput
        v-model="totp" maxlength="6" inputmode="numeric"
        placeholder="6 位验证码" @keyup.enter="submit"
      />
    </template>

    <p v-if="error" class="error">{{ error }}</p>

    <template #footer>
      <ElButton @click="cancel">取消</ElButton>
      <ElButton type="primary" :loading="busy" @click="submit">确认</ElButton>
    </template>
  </ElDialog>
</template>

<style scoped>
.tip {
  margin: 0 0 12px;
  font-size: 12px;
  line-height: 1.7;
  color: #a1a1aa;
}
.field-label {
  display: block;
  margin: 10px 0 6px;
  font-size: 12px;
  color: #a1a1aa;
}
.error {
  margin: 10px 0 0;
  font-size: 12px;
  color: #ff6961;
}
</style>
