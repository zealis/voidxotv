<script setup lang="ts">
// 找回密码（§11.7）：两步——账号+邮箱申请验证码邮件；验证码+新密码完成重置。
// 同一 IP 24 小时 3 次上限，超限需图形验证码；验证码 3 小时有效、最多验证 20 次。
// 需要注意：SMTP 未配置时验证码邮件不会发出（死信留原因），页面会提示联系管理员。
useSeoMeta({ title: '找回密码 · 夜街', robots: 'noindex,nofollow' })

const { api } = useApi()

const step = ref<'request' | 'confirm' | 'done'>('request')
const handle = ref('')
const email = ref('')
const code = ref('')
const next = ref('')
const confirmNext = ref('')
const busy = ref(false)
const errorText = ref('')
const message = ref('')
const captcha = ref<{ id: string; svg: string } | null>(null)
const captchaAnswer = ref('')

async function loadCaptcha() {
  try {
    captcha.value = await api<{ id: string; svg: string }>('/api/v1/auth/captcha')
    captchaAnswer.value = ''
  } catch {
    captcha.value = null
  }
}

async function requestCode() {
  errorText.value = ''
  if (handle.value.trim().length < 3 || !email.value.includes('@')) {
    errorText.value = '请输入账号和注册时使用的邮箱'
    return
  }
  busy.value = true
  try {
    const data = await api<{ message: string }>('/api/v1/auth/password/reset-request', {
      method: 'POST',
      body: {
        handle: handle.value.trim(),
        email: email.value.trim(),
        captcha_id: captcha.value?.id || undefined,
        captcha_answer: captchaAnswer.value || undefined,
      },
    })
    message.value = data.message
    step.value = 'confirm'
  } catch (error) {
    if (error instanceof ApiError && (error.status === 429 || error.code === 'captcha_required')) {
      errorText.value = '请求过于频繁，请先完成图形验证码后再试'
      await loadCaptcha()
    } else {
      errorText.value = error instanceof Error ? error.message : String(error)
    }
  } finally {
    busy.value = false
  }
}

async function confirmReset() {
  errorText.value = ''
  if (code.value.length !== 6) { errorText.value = '请输入 6 位验证码'; return }
  if (next.value.length < 8) { errorText.value = '新密码至少 8 个字符'; return }
  if (next.value !== confirmNext.value) { errorText.value = '两次输入的新密码不一致'; return }
  busy.value = true
  try {
    await api('/api/v1/auth/password/reset', {
      method: 'POST',
      body: {
        handle: handle.value.trim(),
        email: email.value.trim(),
        code: code.value.trim(),
        new_password: next.value,
      },
    })
    step.value = 'done'
  } catch (error) {
    errorText.value = error instanceof Error ? error.message : String(error)
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
        <span class="sub">找回密码</span>
      </header>

      <template v-if="step === 'request'">
        <h1>第一步 · 验证身份</h1>
        <p class="hint">输入账号与注册邮箱，我们会发送 6 位验证码（3 小时内有效）。</p>
        <form @submit.prevent="requestCode">
          <label>
            <span>账号</span>
            <input v-model="handle" autocomplete="username" placeholder="yejie_creator" />
          </label>
          <label>
            <span>注册邮箱</span>
            <input v-model="email" type="email" autocomplete="email" placeholder="you@example.com" />
          </label>
          <template v-if="captcha">
            <label>
              <span>图形验证码（请求频繁时需要）</span>
              <div class="captcha" v-html="captcha.svg" />
              <input v-model="captchaAnswer" autocomplete="off" placeholder="输入图中算式的结果" />
            </label>
          </template>
          <p v-if="errorText" class="error" role="alert">{{ errorText }}</p>
          <button class="primary" type="submit" :disabled="busy">
            {{ busy ? '发送中…' : '发送验证码' }}
          </button>
        </form>
      </template>

      <template v-else-if="step === 'confirm'">
        <h1>第二步 · 设置新密码</h1>
        <p class="hint">{{ message }}</p>
        <p v-if="!captcha" class="hint warn">
          如果迟迟没有收到邮件，可能站点尚未配置 SMTP，请联系管理员。
        </p>
        <form @submit.prevent="confirmReset">
          <label>
            <span>邮箱验证码</span>
            <input v-model="code" inputmode="numeric" maxlength="6" placeholder="6 位数字" />
          </label>
          <label>
            <span>新密码</span>
            <input v-model="next" type="password" autocomplete="new-password" placeholder="至少 8 位" />
          </label>
          <label>
            <span>确认新密码</span>
            <input v-model="confirmNext" type="password" autocomplete="new-password" />
          </label>
          <p v-if="errorText" class="error" role="alert">{{ errorText }}</p>
          <button class="primary" type="submit" :disabled="busy">
            {{ busy ? '提交中…' : '重置密码' }}
          </button>
          <button class="ghost" type="button" @click="step = 'request'">返回上一步</button>
        </form>
      </template>

      <template v-else>
        <h1>密码已重置</h1>
        <p class="hint">所有已登录的会话都已退出，请用新密码重新登录。</p>
        <NuxtLink class="primary link" to="/login">前往登录</NuxtLink>
      </template>

      <footer>验证码只存服务端缓存 · 邮件经队列异步投递</footer>
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
h1 { margin: 24px 0 4px; font-size: 22px; }
.hint { margin: 0 0 18px; font-size: 13px; color: var(--vp-im-text-secondary); }
.hint.warn { color: #ffb86b; }
.captcha { border-radius: 9px; overflow: hidden; border: 1px solid rgba(255, 255, 255, 0.14); }
.captcha :deep(svg) { display: block; width: 100%; height: auto; }
form { display: grid; gap: 16px; }
label { display: grid; gap: 7px; font-size: 12px; color: var(--vp-im-text-secondary); }
input {
  padding: 11px 12px; border-radius: 9px; border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.06); color: var(--vp-im-text-primary); font-size: 14px;
}
input:focus { outline: none; border-color: var(--vp-color-brand); }
.error { margin: 0; font-size: 12px; color: #ff6b5e; }
.primary {
  padding: 11px; border: 0; border-radius: 9px; cursor: pointer;
  background: linear-gradient(135deg, #ff2e7e, #b13bff); color: #fff; font-size: 14px;
}
.primary:disabled { opacity: 0.55; cursor: not-allowed; }
.ghost {
  padding: 10px; border: 1px solid rgba(255, 255, 255, 0.16); border-radius: 9px;
  background: transparent; color: var(--vp-im-text-secondary); cursor: pointer; font-size: 13px;
}
.link { display: block; text-align: center; text-decoration: none; }
footer { margin-top: 22px; font-size: 11px; color: var(--vp-im-text-secondary); text-align: center; }
</style>
