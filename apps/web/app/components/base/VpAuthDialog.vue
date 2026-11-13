<script setup lang="ts">
// 登录 / 注册弹窗：表单与校验沿用原 /login 页，壳换成 VpDialog。
// 登录成功后有 redirect 就导航过去，否则原地重载数据——当前页通常处于
// 「未登录」门禁态（/me、/messages、/library 等），不刷新会继续显示登录按钮。
const { api, clearCsrf } = useApi()
const { state, setMode, close } = useAuthDialog()
const route = useRoute()

const email = ref('')
const password = ref('')
const busy = ref(false)
const errorText = ref('')

// 登录只认「邮箱 + 密码」；夜街号由服务端生成（= 用户 ID），不再让用户起账号。
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

// 关闭时清掉密码与错误，邮箱保留方便再次打开。
watch(() => state.value.open, (open) => {
  errorText.value = ''
  if (!open) password.value = ''
})

function validate(): string {
  if (!EMAIL_PATTERN.test(email.value.trim())) return '请输入有效的邮箱地址'
  if (password.value.length < 8) return '密码至少 8 位'
  return ''
}

async function submit() {
  errorText.value = ''
  const invalid = validate()
  if (invalid) {
    errorText.value = invalid
    return
  }
  const account = email.value.trim().toLowerCase()
  busy.value = true
  try {
    if (state.value.mode === 'register') {
      await api('/api/v1/auth/register', {
        method: 'POST',
        body: { email: account, password: password.value },
      })
    }
    clearCsrf()
    await api('/api/v1/auth/login', {
      method: 'POST',
      body: { email: account, password: password.value, device_id: 'web' },
    })
    const target = state.value.redirect
    close()
    if (target) await navigateTo(target)
    else await reloadNuxtApp({ path: route.fullPath, persistState: false })
  } catch (error) {
    errorText.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

function goReset() {
  close()
  void navigateTo('/reset-password')
}
</script>

<template>
  <VpDialog
    :open="state.open"
    :title="state.mode === 'login' ? '登录夜街' : '注册夜街'"
    :busy="busy"
    initial-focus=".field input"
    :data-surface="state.surface"
    @close="close"
  >
    <p v-if="state.reason" class="reason">{{ state.reason }}</p>

    <div class="tabs" role="tablist" aria-label="登录或注册">
      <button
        type="button" role="tab" :aria-selected="state.mode === 'login'"
        :class="{ active: state.mode === 'login' }" @click="setMode('login')"
      >登录</button>
      <button
        type="button" role="tab" :aria-selected="state.mode === 'register'"
        :class="{ active: state.mode === 'register' }" @click="setMode('register')"
      >注册</button>
    </div>

    <form @submit.prevent="submit">
      <label class="field">
        <span>邮箱</span>
        <input
          v-model="email" type="email" inputmode="email" autocomplete="username"
          placeholder="you@example.com"
        />
      </label>
      <label class="field">
        <span>密码</span>
        <input
          v-model="password" type="password"
          :autocomplete="state.mode === 'register' ? 'new-password' : 'current-password'"
          placeholder="至少 8 位"
        />
      </label>

      <p v-if="errorText" class="err" role="alert">{{ errorText }}</p>

      <button class="submit" type="submit" :disabled="busy">
        {{ busy ? '处理中…' : state.mode === 'login' ? '登录' : '注册并登录' }}
      </button>
    </form>

    <p v-if="state.mode === 'login'" class="forgot">
      <button type="button" class="link" @click="goReset">忘记密码？</button>
    </p>
  </VpDialog>
</template>

<style scoped>
.reason {
  margin: -4px 0 14px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--vp-tool-text-2);
}
.tabs { display: flex; gap: 8px; margin-bottom: 16px; }
.tabs button {
  flex: 1;
  padding: 8px;
  border-radius: 9px;
  border: 1px solid var(--vp-tool-dialog-border);
  background: transparent;
  color: var(--vp-tool-text-2);
  font-size: 13px;
  cursor: pointer;
}
.tabs button.active {
  border-color: var(--vp-color-brand);
  background: rgba(255, 46, 126, 0.14);
  color: var(--vp-tool-text);
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
.submit {
  margin-top: 4px;
  padding: 11px;
  border: 0;
  border-radius: 10px;
  background: var(--vp-color-brand);
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}
.submit:disabled { opacity: 0.6; cursor: not-allowed; }
.forgot { margin: 12px 0 0; text-align: right; }
.link {
  border: 0;
  background: none;
  padding: 0;
  color: var(--vp-tool-text-2);
  font-size: 12px;
  cursor: pointer;
  text-decoration: underline;
}
</style>
