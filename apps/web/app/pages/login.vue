<script setup lang="ts">
useSeoMeta({ robots: 'noindex,nofollow' })
// 桌面端登录 / 注册入口（Page 3 深色后台基线）。会话 Cookie 由同源 Go API 下发。
const { api, clearCsrf } = useApi()

const mode = ref<'login' | 'register'>('login')
const handle = ref('')
const email = ref('')
const password = ref('')
const busy = ref(false)
const errorText = ref('')
const route = useRoute()

// 登录后回到来源页：消息中心、草稿箱、设置等 7 处入口都带 redirect。
// 只接受站内绝对路径（单斜杠开头），挡掉 //evil.com 与 /\evil.com 这类开放重定向。
function redirectTarget(): string {
  const value = route.query.redirect
  const raw = Array.isArray(value) ? value[0] : value
  if (typeof raw !== 'string') return '/publish'
  const path = raw.trim()
  if (!path.startsWith('/') || path.startsWith('//') || path.startsWith('/\\')) return '/publish'
  return path
}

async function submit() {
  errorText.value = ''
  const name = handle.value.trim()
  if (!/^[a-zA-Z0-9_]{3,32}$/.test(name)) {
    errorText.value = '账号需为 3–32 位字母、数字或下划线'
    return
  }
  if (password.value.length < 8) {
    errorText.value = '密码至少 8 位'
    return
  }
  busy.value = true
  try {
    if (mode.value === 'register') {
      await api('/api/v1/auth/register', {
        method: 'POST',
        body: { handle: name, password: password.value, email: email.value.trim() || undefined },
      })
    }
    clearCsrf()
    await api('/api/v1/auth/login', {
      method: 'POST',
      body: { handle: name, password: password.value, device_id: 'web-publish' },
    })
    await navigateTo(redirectTarget())
  } catch (error) {
    errorText.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="auth-shell" data-surface="immersive">
    <section class="auth-card">
      <header class="brand-row">
        <NuxtLink to="/" class="brand">夜街</NuxtLink>
        <span class="sub">创作者工作台</span>
      </header>

      <h1>{{ mode === 'login' ? '登录' : '创建账号' }}</h1>
      <p class="hint">登录后即可上传、转码并发布作品。</p>

      <div class="tabs" role="tablist">
        <button type="button" :class="{ active: mode === 'login' }" @click="mode = 'login'">登录</button>
        <button type="button" :class="{ active: mode === 'register' }" @click="mode = 'register'">注册</button>
      </div>

      <form @submit.prevent="submit">
        <label>
          <span>账号</span>
          <input v-model="handle" autocomplete="username" placeholder="yejie_creator" />
        </label>
        <label v-if="mode === 'register'">
          <span>邮箱（可选）</span>
          <input v-model="email" type="email" autocomplete="email" placeholder="you@example.com" />
        </label>
        <label>
          <span>密码</span>
          <input v-model="password" type="password" autocomplete="current-password" placeholder="至少 8 位" />
        </label>

        <p v-if="errorText" class="error" role="alert">{{ errorText }}</p>
        <button class="primary" type="submit" :disabled="busy">
          {{ busy ? '处理中…' : mode === 'login' ? '登录并前往发布' : '注册并登录' }}
        </button>
      </form>

      <p v-if="mode === 'login'" class="reset-link">
        <NuxtLink to="/reset-password">忘记密码？</NuxtLink>
      </p>

      <footer>会话使用 HttpOnly Cookie · 写操作需 CSRF 双提交校验</footer>
    </section>
  </main>
</template>

<style scoped>
.auth-shell {
  min-height: 100dvh;
  display: grid;
  place-items: center;
  padding: 32px;
  background: radial-gradient(circle at 20% 0%, rgba(255, 46, 126, 0.14), transparent 45%), var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.auth-card {
  width: min(420px, 100%);
  padding: 32px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 18px;
  background: rgba(18, 18, 42, 0.82);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.45);
}
.brand-row { display: flex; align-items: baseline; gap: 12px; }
.brand { color: var(--vp-color-brand); font-size: 22px; font-weight: 700; letter-spacing: 0.1em; text-decoration: none; }
.sub { color: var(--vp-im-text-secondary); font-size: 12px; }
h1 { margin: 28px 0 4px; font-size: 26px; }
.hint { margin: 0 0 20px; font-size: 13px; color: var(--vp-im-text-secondary); }
.tabs { display: flex; gap: 8px; margin-bottom: 22px; }
.tabs button {
  flex: 1; padding: 9px; border: 1px solid rgba(255,255,255,.12); border-radius: 9px;
  background: transparent; color: var(--vp-im-text-secondary); cursor: pointer; font-size: 13px;
}
.tabs button.active { border-color: var(--vp-color-brand); color: #fff; background: rgba(255, 46, 126, 0.16); }
form { display: grid; gap: 16px; }
.reset-link { margin: 14px 0 0; text-align: right; font-size: 12px; }
.reset-link a { color: var(--vp-im-text-secondary); }
label { display: grid; gap: 7px; font-size: 12px; color: var(--vp-im-text-secondary); }
input {
  padding: 11px 12px; border-radius: 9px; border: 1px solid rgba(255,255,255,.14);
  background: rgba(6, 6, 20, 0.75); color: #fff; font-size: 14px; outline: none;
}
input:focus { border-color: var(--vp-color-brand); }
.primary {
  margin-top: 6px; padding: 12px; border: 0; border-radius: 10px; cursor: pointer;
  background: var(--vp-color-brand); color: #fff; font-weight: 600; font-size: 14px;
}
.primary:disabled { opacity: 0.6; cursor: not-allowed; }
.error { margin: 0; padding: 9px 11px; border-radius: 8px; background: rgba(255, 70, 70, 0.14); color: #ffb4b4; font-size: 12px; }
footer { margin-top: 22px; font-size: 11px; color: rgba(255, 255, 255, 0.42); text-align: center; }
</style>
