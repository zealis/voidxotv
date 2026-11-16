<script setup lang="ts">
// 敏感操作的身份确认弹窗：前台与后台共用，按 state.scope 调对应接口。
// 后台开了 TOTP 的账号会先收到 totp_required，再显示验证码输入框。
// 壳与登录弹窗一致走 VpDialog（UI 规范 §11.3：内容页只用 Vp* 封装组件）。
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
  <VpDialog
    :open="state.open" title="确认身份" :busy="busy"
    :data-surface="state.surface"
    initial-focus=".field input" @close="cancel"
  >
    <p class="tip">{{ state.reason }}</p>

    <form @submit.prevent="submit">
      <label class="field">
        <span>登录密码</span>
        <input
          v-model="password" type="password" autocomplete="current-password"
          placeholder="请输入当前登录密码"
        />
      </label>

      <label v-if="needTotp" class="field">
        <span>动态验证码</span>
        <input
          v-model="totp" maxlength="6" inputmode="numeric"
          autocomplete="one-time-code" placeholder="6 位验证码"
        />
      </label>

      <p v-if="error" class="err" role="alert">{{ error }}</p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="cancel">取消</button>
        <button type="submit" class="btn primary" :disabled="busy">
          {{ busy ? '验证中…' : '确认' }}
        </button>
      </div>
    </form>
  </VpDialog>
</template>

<style scoped>
.tip {
  margin: -4px 0 14px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--vp-tool-text-2);
}
form { display: grid; gap: 12px; }
.field { display: grid; gap: 6px; }
.field span { font-size: 12px; color: var(--vp-tool-text-2); }
.field input {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  border-radius: 9px;
  border: 1px solid var(--vp-tool-input-border);
  background: var(--vp-tool-input);
  color: var(--vp-tool-text);
  font-size: 14px;
  outline: none;
}
.field input:focus-visible { border-color: var(--vp-color-brand); }
.field input::placeholder { color: var(--vp-tool-text-2); opacity: 0.7; }
.err {
  margin: 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--vp-tool-danger-soft);
  color: var(--vp-tool-danger-ink);
  font-size: 12px;
}
.dialog-actions { display: flex; gap: 10px; justify-content: flex-end; }
.btn {
  padding: 9px 18px;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  border: 1px solid var(--vp-tool-border);
  background: transparent;
  color: inherit;
}
.btn.primary {
  background: var(--vp-tool-accent);
  border-color: transparent;
  color: var(--vp-tool-on-accent);
}
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
