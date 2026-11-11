<script setup lang="ts">
import { ElAlert, ElButton, ElForm, ElFormItem, ElInput } from 'element-plus'

const emit = defineEmits<{ success: [] }>()
const { login } = useAdminApi()

const username = ref('')
const password = ref('')
const totpCode = ref('')
const needTotp = ref(false)
const pending = ref(false)
const error = ref('')

async function submit() {
  if (pending.value) return
  error.value = ''
  if (!username.value.trim() || !password.value) {
    error.value = '请输入后台账号与密码'
    return
  }
  pending.value = true
  try {
    await login(username.value.trim(), password.value, totpCode.value.trim() || undefined)
    password.value = ''
    totpCode.value = ''
    needTotp.value = false
    emit('success')
  } catch (cause) {
    // 服务端要求动态码（§11.6）：request 抛出的 message 即服务端文案。
    if (/动态验证码/.test(cause instanceof Error ? cause.message : String(cause))) {
      needTotp.value = true
    }
    error.value = cause instanceof Error ? cause.message : '登录失败'
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <div class="admin-login">
    <div class="login-card">
      <div class="brand">
        <span class="mark">夜</span>
        <div>
          <strong>夜街 · 运营后台</strong>
          <p>管理账号与普通用户体系分离，权限在服务端校验</p>
        </div>
      </div>

      <ElAlert v-if="error" :title="error" type="error" :closable="false" show-icon />

      <ElForm label-position="top" @submit.prevent="submit">
        <ElFormItem label="管理员账号">
          <ElInput v-model="username" placeholder="如 yj_reviewer" autocomplete="username" />
        </ElFormItem>
        <ElFormItem label="密码">
          <ElInput
            v-model="password" type="password" show-password
            placeholder="后台独立密码" autocomplete="current-password"
            @keyup.enter="submit"
          />
        </ElFormItem>
        <ElFormItem v-if="needTotp" label="动态验证码（TOTP）">
          <ElInput
            v-model="totpCode" inputmode="numeric" maxlength="6"
            placeholder="6 位动态码" @keyup.enter="submit"
          />
        </ElFormItem>
        <ElButton type="primary" class="submit" :loading="pending" @click="submit">
          登录后台
        </ElButton>
      </ElForm>

      <p class="hint">
        开发环境账号由 <code>go run ./cmd/seed</code> 创建；生产环境不提供演示账号。
      </p>
    </div>
  </div>
</template>

<style scoped>
.admin-login {
  min-height: 100dvh;
  display: grid;
  place-items: center;
  padding: 24px;
  background:
    radial-gradient(1200px 600px at 15% -10%, rgba(255, 46, 126, 0.16), transparent 60%),
    radial-gradient(900px 500px at 90% 110%, rgba(34, 211, 238, 0.12), transparent 60%),
    #0b0b0d;
  color: #f5f5f7;
}
.login-card {
  width: min(420px, 100%);
  display: grid;
  gap: 18px;
  padding: 28px;
  border-radius: 18px;
  background: rgba(22, 22, 26, 0.92);
  border: 1px solid rgba(255, 255, 255, 0.08);
}
.brand { display: flex; gap: 12px; align-items: center; }
.mark {
  width: 40px; height: 40px; border-radius: 12px;
  display: grid; place-items: center;
  background: linear-gradient(135deg, #ff2e7e, #ff6aa2);
  font-weight: 700;
}
.brand strong { display: block; font-size: 16px; }
.brand p { margin: 4px 0 0; font-size: 12px; color: #a1a1aa; }
.submit { width: 100%; }
.hint { margin: 0; font-size: 12px; line-height: 1.6; color: #8b8b93; }
.hint code { color: #d4d4d8; }
</style>
