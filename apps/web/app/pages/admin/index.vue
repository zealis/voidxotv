<script setup lang="ts">
import { ElButton } from 'element-plus'
import 'element-plus/dist/index.css'
// Element Plus 的深色变量只在引入该文件后才会随 .dark 生效。
import 'element-plus/theme-chalk/dark/css-vars.css'

// 后台是私有工具页：客户端渲染避免 SSR 跨请求共享会话，且不参与索引。
definePageMeta({ ssr: false })
useSeoMeta({
  title: '运营后台 · 夜街',
  robots: 'noindex,nofollow' })

const { session, booting, loadSession, logout, can } = useAdminApi()

const modules = [
  { key: 'overview', label: '数据概览', icon: 'chart', permission: 'stats:read' },
  { key: 'review', label: '内容审核', icon: 'shield', permission: 'reviews:read' },
  { key: 'users', label: '用户管理', icon: 'user', permission: 'users:read' },
  { key: 'topics', label: '话题运营', icon: 'hash', permission: 'topics:read' },
  { key: 'reports', label: '举报中心', icon: 'flag', permission: 'reports:read' },
  { key: 'settings', label: '系统设置', icon: 'settings', permission: 'settings:read' },
  { key: 'flags', label: '功能开关', icon: 'toggle', permission: 'settings:read' },
  { key: 'accounts', label: '账号权限', icon: 'lock', permission: 'accounts:read' },
] as const

type ModuleKey = (typeof modules)[number]['key']

const route = useRoute()
const active = ref<ModuleKey>('overview')

// 模块可深链（?tab=accounts）：刷新或分享链接后停在同一个模块。
watch(active, (value) => {
  if (route.query.tab === value) return
  navigateTo({ query: { ...route.query, tab: value } }, { replace: true })
})
const focusReviewId = ref<number | undefined>(undefined)

const available = computed(() => modules.filter((item) => can(item.permission)))
const current = computed(() => (
  modules.find((item) => item.key === active.value) || modules[0]
))

const roleLabels: Record<string, string> = {
  super_admin: '超级管理员', reviewer: '审核员', operator: '运营', readonly: '只读' }

function openReview(contentId: number) {
  focusReviewId.value = contentId
  active.value = 'review'
}

async function signOut() {
  await logout()
  active.value = 'overview'
}

onMounted(async () => {
  document.documentElement.classList.add('dark')
  await loadSession()
  if (session.value) {
    const permitted = available.value.map((item) => item.key)
    const requested = typeof route.query.tab === 'string' ? route.query.tab : ''
    if (requested && permitted.includes(requested as ModuleKey)) {
      active.value = requested as ModuleKey
    }
    if (!permitted.includes(active.value) && permitted.length) {
      active.value = permitted[0] as ModuleKey
    }
  }
})

onBeforeUnmount(() => {
  document.documentElement.classList.remove('dark')
})
</script>

<template>
  <div v-if="booting" class="boot">正在校验后台会话…</div>

  <AdminLogin v-else-if="!session" />

  <div v-else class="admin-shell">
    <aside class="nav">
      <div class="brand">
        <span class="mark">夜</span>
        <div>
          <strong>夜街</strong>
          <p>运营后台</p>
        </div>
      </div>

      <nav class="nav-list" aria-label="后台模块">
        <button
          v-for="item in available" :key="item.key" type="button"
          :class="['nav-item', { on: active === item.key }]"
          @click="active = item.key"
        >
          <span class="nav-mark" aria-hidden="true"><ImIcon :name="item.icon" :size="15" /></span>
          <span>{{ item.label }}</span>
        </button>
      </nav>

      <div class="nav-footer">
        <!-- 侧栏会话卡片不用 .admin-card：那是全局列表卡片的类名，会多画一圈边框。 -->
        <div class="session-card">
          <span class="avatar">{{ session.email.slice(0, 1).toUpperCase() }}</span>
          <div class="admin-meta">
            <strong>{{ session.email }}</strong>
            <span>{{ roleLabels[session.role] || session.role }}</span>
          </div>
        </div>
        <div class="footer-actions">
          <AdminPasswordDialog />
          <ElButton size="small" class="logout" @click="signOut">退出登录</ElButton>
        </div>
      </div>
    </aside>

    <main class="main">
      <header class="page-head">
        <h1>{{ current.label }}</h1>
        <span class="role-chip">{{ roleLabels[session.role] || session.role }}</span>
      </header>

      <AdminOverview v-if="active === 'overview'" @open-review="openReview" />
      <AdminReview v-else-if="active === 'review'" :focus-id="focusReviewId" />
      <AdminUsers v-else-if="active === 'users'" />
      <AdminTopics v-else-if="active === 'topics'" />
      <AdminReports v-else-if="active === 'reports'" />
      <AdminSettings v-else-if="active === 'settings'" />
      <AdminFeatureFlags v-else-if="active === 'flags'" />
      <AdminAccounts v-else-if="active === 'accounts'" />
      <p v-else class="empty">当前角色没有可访问的模块。</p>
    </main>
  </div>
</template>

<style scoped>
.boot {
  min-height: 100dvh; display: grid; place-items: center;
  background: #0b0b0d; color: #a1a1aa; font-size: 13px;
}
.admin-shell {
  min-height: 100dvh;
  display: grid;
  grid-template-columns: 236px minmax(0, 1fr);
  background: #0b0b0d;
  color: #e4e4e7;
}
.nav {
  display: flex; flex-direction: column; gap: 18px;
  /* 100dvh 必须配 border-box，否则上下 padding 会把侧栏撑出视口，
     底部的「修改密码 / 退出登录」被裁掉。 */
  box-sizing: border-box;
  padding: 20px 14px 16px;
  background: #0f0f12;
  border-right: 1px solid rgba(255, 255, 255, 0.07);
  position: sticky; top: 0; height: 100dvh;
}
.brand { display: flex; gap: 10px; align-items: center; padding: 0 6px; }
.mark {
  width: 34px; height: 34px; border-radius: 10px;
  display: grid; place-items: center;
  background: linear-gradient(135deg, #ff2e7e, #ff6aa2);
  font-weight: 700; font-size: 15px; color: #fff;
}
.brand strong { display: block; font-size: 14px; color: #fafafa; }
.brand p { margin: 1px 0 0; font-size: 11px; color: #8b8b93; }
/* 模块变多时让列表自己滚，底部账号区永远贴在视口内。 */
.nav-list { display: grid; gap: 4px; overflow-y: auto; min-height: 0; }
.nav-item {
  display: flex; align-items: center; gap: 10px;
  padding: 9px 10px; border-radius: 10px;
  background: transparent; border: 0; cursor: pointer;
  color: #a1a1aa; font-size: 13px; text-align: left;
  transition: background var(--vp-motion-exit) var(--vp-ease-out), color var(--vp-motion-exit) var(--vp-ease-out);
}
.nav-item:hover { background: rgba(255, 255, 255, 0.04); color: #e4e4e7; }
.nav-item.on { background: rgba(255, 46, 126, 0.14); color: #fff; }
.nav-mark {
  width: 22px; height: 22px; border-radius: 7px;
  display: grid; place-items: center;
  background: rgba(255, 255, 255, 0.06);
  color: #a1a1aa;
  transition: background var(--vp-motion-exit) var(--vp-ease-out), color var(--vp-motion-exit) var(--vp-ease-out);
}
.nav-item:hover .nav-mark { color: #e4e4e7; }
.nav-item.on .nav-mark { background: rgba(255, 46, 126, 0.28); color: #fff; }
.nav-footer { margin-top: auto; display: grid; gap: 8px; }
.session-card {
  display: flex; gap: 8px; align-items: center;
  padding: 8px 10px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.03);
}
.avatar {
  width: 28px; height: 28px; border-radius: 50%;
  display: grid; place-items: center;
  background: rgba(34, 211, 238, 0.16); color: #67e8f9;
  font-size: 12px; font-weight: 700;
}
.admin-meta { display: grid; line-height: 1.3; min-width: 0; }
.admin-meta strong { font-size: 12px; color: #f4f4f5; overflow: hidden; text-overflow: ellipsis; }
.admin-meta span { font-size: 11px; color: #8b8b93; }
.logout { width: 100%; }
.footer-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
.footer-actions :deep(.el-button) { width: 100%; margin: 0; }
.main {
  padding: 24px 28px 40px;
  min-width: 0;
  background:
    radial-gradient(900px 420px at 12% -10%, rgba(255, 46, 126, 0.08), transparent 60%),
    radial-gradient(720px 360px at 96% 0%, rgba(34, 211, 238, 0.06), transparent 60%),
    #0b0b0d;
}
.page-head {
  display: flex; align-items: center; justify-content: space-between;
  gap: 16px; margin-bottom: 18px;
}
.page-head h1 { margin: 0; font-size: 22px; letter-spacing: -0.01em; color: #fafafa; }
.role-chip {
  padding: 4px 10px; border-radius: 99px; font-size: 11px;
  background: rgba(255, 255, 255, 0.06); color: #d4d4d8; white-space: nowrap;
}
.empty { font-size: 13px; color: #8b8b93; }
@media (max-width: 860px) {
  .admin-shell { grid-template-columns: 1fr; }
  .nav { position: static; height: auto; }
  .nav-list { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .main { padding: 18px 16px 32px; }
}
</style>

<!-- 后台固定深色：把 Element Plus 深色变量映射到品牌令牌（全局，覆盖弹层/表单）。 -->
<style>
html.dark {
  --el-color-primary: #ff2e7e;
  --el-color-primary-light-3: #ff5c97;
  --el-color-primary-light-5: #ff8ab5;
  --el-color-primary-light-7: #ffb3ce;
  --el-color-primary-light-8: #ffc9dc;
  --el-color-primary-light-9: #2a1420;
  --el-color-primary-dark-2: #d81f68;
  --el-bg-color: #141417;
  --el-bg-color-page: #0b0b0d;
  --el-bg-color-overlay: #1a1a1f;
  --el-fill-color-blank: #17171b;
  --el-fill-color-light: rgba(255, 255, 255, 0.06);
  --el-fill-color: rgba(255, 255, 255, 0.04);
  --el-text-color-primary: #f4f4f5;
  --el-text-color-regular: #d4d4d8;
  --el-text-color-secondary: #a1a1aa;
  --el-text-color-placeholder: #71717a;
  --el-border-color: rgba(255, 255, 255, 0.12);
  --el-border-color-light: rgba(255, 255, 255, 0.09);
  --el-border-color-lighter: rgba(255, 255, 255, 0.07);
  --el-mask-color: rgba(0, 0, 0, 0.72);
}
/* 弹窗宽度是各组件写死的 400/440/520px，手机上会顶出屏幕右缘；
   统一压一个上限，等宽布局里就不用每个弹窗各自算 rem。 */
html.dark .el-dialog {
  max-width: calc(100vw - 32px);
}
html.dark .el-input__wrapper,
html.dark .el-textarea__inner {
  background-color: #17171b;
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.1) inset;
}
/* 焦点环必须贴在可见边框上。Element Plus 的 <input> 被包在带内边距的
   .el-input__wrapper 里，tokens.css 的全局 :focus-visible 环会画在内层输入框上，
   比边框缩进一圈；这里让内层不画环，改由 wrapper 自己的焦点态呈现。
   注意上面那条静态 box-shadow 的优先级高于 EP 的 .is-focus，必须显式补状态。 */
html.dark .el-input__inner:focus-visible,
html.dark .el-input__inner:focus,
html.dark .el-textarea__inner:focus-visible {
  box-shadow: none;
}
html.dark .el-input__wrapper:hover {
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.22) inset;
}
html.dark .el-input__wrapper.is-focus,
html.dark .el-input__wrapper:focus-within {
  box-shadow:
    0 0 0 1px var(--el-color-primary) inset,
    0 0 0 3px rgba(255, 46, 126, 0.18);
}
html.dark .el-textarea__inner:focus {
  box-shadow:
    0 0 0 1px var(--el-color-primary) inset,
    0 0 0 3px rgba(255, 46, 126, 0.18);
}
html.dark .el-select__wrapper.is-focused {
  box-shadow:
    0 0 0 1px var(--el-color-primary) inset,
    0 0 0 3px rgba(255, 46, 126, 0.18);
}
html.dark .el-table {
  --el-table-border-color: rgba(255, 255, 255, 0.07);
  --el-table-header-bg-color: rgba(255, 255, 255, 0.03);
  --el-table-row-hover-bg-color: rgba(255, 46, 126, 0.08);
  --el-table-text-color: #d4d4d8;
  --el-table-header-text-color: #a1a1aa;
}
</style>
