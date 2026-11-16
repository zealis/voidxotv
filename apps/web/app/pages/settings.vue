<script setup lang="ts">
// 设置页（UI 规范 §11.2）：按 账号 / 隐私 / 通知 / 播放与材质 / 通用 分组。
// 工具型信息密度优先，行高与对齐保持一致；控件用内容页的 Vp* 组件，
// 不把 Element Plus 的默认主题带进前台。
import { ApiError, errorMessage } from '~/composables/useApi'
import type { ToolTheme } from '~/composables/usePreferences'

useSeoMeta({ title: '设置 · 夜街', robots: 'noindex,nofollow' })

// 版本号来自 nuxt.config 读的 apps/web/package.json，与仓库根 VERSION 由
// `node scripts/bump-version.mjs` 一起改，页面只负责展示。
const appVersion = useRuntimeConfig().public.appVersion

interface Settings {
  notify_interaction: boolean
  notify_follow: boolean
  notify_system: boolean
  quiet_start: number
  quiet_end: number
  default_visibility: 'public' | 'friends' | 'private'
  autoplay: boolean
  reduce_motion: boolean
  theme: ToolTheme
}

interface Profile {
  handle: string
  display_name: string
  bio: string
  email: string
  avatar_url?: string
}

const { api, upload, getMe } = useApi()

const { open: openAuthDialog } = useAuthDialog()
const { preferences, applyMotion, setTheme, adoptTheme, resolveTheme } = usePreferences()
// 加载态由状态机统一管理；message 仍用于写操作（保存 / 改密）失败提示。
const asyncState = useAsyncState()
const { phase, errorText, retrying } = asyncState

const settings = ref<Settings | null>(null)
const profile = ref<Profile | null>(null)
const displayName = ref('')
const bio = ref('')
// 头像：选完即上传，成功后用服务端返回的版本化地址覆盖预览。
const avatarUrl = ref('')
const avatarPreview = ref('')
const avatarBusy = ref(false)
// 移除头像走统一二次确认弹窗：确认后仍需密码 step-up（服务端闸门）。
const avatarConfirming = ref(false)
const quietOn = ref(false)
const quietStart = ref(1320)
const quietEnd = ref(480)

// 随机头像 6 选 1：预览走同源代理，选定后才由后端下载并存进头像体系。
const randomAvatarOpen = ref(false)
const pickedSeed = ref('')
const randomAvatarBusy = ref(false)

function openRandomAvatar() {
  pickedSeed.value = ''
  randomAvatarOpen.value = true
}

async function applyRandomAvatar() {
  if (!pickedSeed.value) return
  randomAvatarBusy.value = true
  message.value = ''
  try {
    const result = await api<{ avatar_url: string }>('/api/v1/me/avatar/random', {
      method: 'POST', body: { seed: pickedSeed.value },
    })
    avatarUrl.value = result?.avatar_url || ''
    if (profile.value) profile.value.avatar_url = avatarUrl.value
    clearAvatarPreview()
    randomAvatarOpen.value = false
    message.value = '头像已更新'
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    randomAvatarBusy.value = false
  }
}

const loading = ref(true)
const saving = ref(false)
const profileSaving = ref(false)
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')
const saved = ref('')
let savedTimer: ReturnType<typeof setTimeout> | null = null

const pwd = reactive({
  open: false, current: '', next: '', confirm: '', busy: false, error: '',
})

const visibilityOptions = [
  { value: 'public', label: '公开' },
  { value: 'friends', label: '仅互关好友' },
  { value: 'private', label: '仅自己' },
]

// §14：主题只作用于工具型页面；沉浸界面恒深色，不受此设置影响。
const themeOptions: { value: ToolTheme; label: string; hint: string }[] = [
  { value: 'system', label: '跟随系统', hint: '按设备的浅色 / 深色偏好自动切换' },
  { value: 'light', label: '浅色', hint: '工具页使用独立设计的浅色色板' },
  { value: 'dark', label: '深色', hint: '工具页保持深色基线' },
]

const currentTheme = computed<ToolTheme>(() => settings.value?.theme ?? 'system')
const themeResolved = computed(() => resolveTheme(currentTheme.value) === 'light' ? '浅色' : '深色')
const themeHint = computed(() =>
  themeOptions.find((item) => item.value === currentTheme.value)?.hint ?? '')

function onThemeChange(value: ToolTheme) {
  // 立即应用到 DOM（不等接口返回），失败时 save() 会以服务端值为准回滚。
  setTheme(value)
  void save({ theme: value })
}

function clock(minute: number): string {
  const hour = Math.floor(minute / 60)
  return `${String(hour).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
}

// 免打扰用 30 分钟粒度：既能表达常见作息，又不必引入时间选择器的复杂度。
const timeOptions = Array.from({ length: 48 }, (_, index) => ({
  value: index * 30,
  label: clock(index * 30),
}))

async function load() {
  loading.value = true
  message.value = ''
  asyncState.begin()
  try {
    const [value, owner] = await Promise.all([
      api<Settings>('/api/v1/me/settings'),
      api<Profile>('/api/v1/me/profile'),
    ])
    applySettings(value)
    profile.value = owner
    displayName.value = owner.display_name
    bio.value = owner.bio
    avatarUrl.value = owner.avatar_url || ''
    asyncState.succeed()
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
      asyncState.succeed() // 交给登录引导，不再叠一层错误态
    } else {
      asyncState.fail(error)
    }
  } finally {
    loading.value = false
  }
}

const AVATAR_TYPES = ['image/png', 'image/jpeg', 'image/webp']
const AVATAR_MAX_BYTES = 2 * 1024 * 1024

function clearAvatarPreview() {
  if (avatarPreview.value) URL.revokeObjectURL(avatarPreview.value)
  avatarPreview.value = ''
}

async function onAvatarPick(event: Event) {
  const input = event.target as HTMLInputElement
  const picked = input.files?.[0]
  input.value = ''
  if (!picked) return
  message.value = ''
  if (!AVATAR_TYPES.includes(picked.type)) {
    message.value = '头像仅支持 PNG / JPEG / WebP'
    return
  }
  if (picked.size > AVATAR_MAX_BYTES) {
    message.value = '头像图片不能超过 2 MiB'
    return
  }
  clearAvatarPreview()
  avatarPreview.value = URL.createObjectURL(picked)
  avatarBusy.value = true
  try {
    const form = new FormData()
    form.append('image', picked, picked.name || 'avatar.png')
    const result = await upload<{ avatar_url: string }>('/api/v1/me/avatar', form)
    avatarUrl.value = result?.avatar_url || ''
    if (profile.value) profile.value.avatar_url = avatarUrl.value
    message.value = '头像已更新'
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    avatarBusy.value = false
    clearAvatarPreview()
  }
}

async function removeAvatar() {
  if (avatarBusy.value) return
  avatarBusy.value = true
  message.value = ''
  try {
    await api('/api/v1/me/avatar', { method: 'DELETE' })
    avatarConfirming.value = false
    avatarUrl.value = ''
    if (profile.value) profile.value.avatar_url = ''
    message.value = '已恢复默认头像'
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    avatarBusy.value = false
  }
}

function applySettings(value: Settings) {
  settings.value = value
  quietOn.value = value.quiet_start !== value.quiet_end
  quietStart.value = value.quiet_start
  quietEnd.value = value.quiet_end
  // 同步到全局偏好：播放器、动效与主题立即跟着变。
  preferences.value.autoplay = value.autoplay
  preferences.value.reduceMotion = value.reduce_motion
  preferences.value.defaultVisibility = value.default_visibility
  applyMotion()
  // 服务端是权威值：登录 / 保存后覆盖本地缓存（换设备也一致）。
  adoptTheme(value.theme)
}

function flashSaved(text = '已保存') {
  saved.value = text
  if (savedTimer) clearTimeout(savedTimer)
  savedTimer = setTimeout(() => { saved.value = '' }, 1800)
}

async function save(patch: Record<string, unknown>) {
  if (saving.value) return
  saving.value = true
  message.value = ''
  try {
    applySettings(await api<Settings>('/api/v1/me/settings', { method: 'PATCH', body: patch }))
    flashSaved()
  } catch (error) {
    message.value = errorMessage(error)
    // 失败时以服务端为准，避免开关停在一个并未生效的位置。
    await load()
  } finally {
    saving.value = false
  }
}

function onVisibilityChange(value: string | number) {
  void save({ default_visibility: String(value) })
}

function toggleQuiet(next: boolean) {
  quietOn.value = next
  // 打开时给出 22:00–08:00 的常见默认；关闭时两端归零，服务端据此判定为「未启用」。
  void save(next
    ? { quiet_start: quietStart.value === quietEnd.value ? 1320 : quietStart.value, quiet_end: quietEnd.value === 0 ? 480 : quietEnd.value }
    : { quiet_start: 0, quiet_end: 0 })
}

function onQuietStartChange(value: string | number) {
  quietStart.value = Number(value)
  void save({ quiet_start: quietStart.value, quiet_end: quietEnd.value === quietStart.value ? (quietStart.value + 60) % 1440 : quietEnd.value })
}

function onQuietEndChange(value: string | number) {
  quietEnd.value = Number(value)
  void save({ quiet_start: quietStart.value, quiet_end: quietEnd.value })
}

async function saveProfile() {
  const name = displayName.value.trim()
  if (!name) {
    message.value = '昵称不能为空'
    return
  }
  if (name.length > 24) {
    message.value = '昵称不能超过 24 个字符'
    return
  }
  profileSaving.value = true
  message.value = ''
  try {
    profile.value = await api<Profile>('/api/v1/me/profile', {
      method: 'PATCH', body: { display_name: name, bio: bio.value.trim() },
    })
    displayName.value = profile.value.display_name
    bio.value = profile.value.bio
    flashSaved('资料已保存')
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    profileSaving.value = false
  }
}

function openPassword() {
  pwd.open = true
  pwd.current = ''
  pwd.next = ''
  pwd.confirm = ''
  pwd.error = ''
}

async function changePassword() {
  pwd.error = ''
  if (pwd.next.length < 8) { pwd.error = '新密码至少 8 个字符'; return }
  if (pwd.next !== pwd.confirm) { pwd.error = '两次输入的新密码不一致'; return }
  if (pwd.next === pwd.current) { pwd.error = '新密码不能与当前密码相同'; return }
  pwd.busy = true
  try {
    await api('/api/v1/me/password', {
      method: 'POST',
      body: { current_password: pwd.current, new_password: pwd.next },
    })
    pwd.open = false
    flashSaved('密码已更新，请重新登录')
    // 服务端已吊销全部会话（§11.4）：回登录页重新认证。
    setTimeout(() => { openAuthDialog('login', { reason: '密码已更新，请重新登录' }) }, 1200)
  } catch (error) {
    pwd.error = errorMessage(error)
  } finally {
    pwd.busy = false
  }
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
    loading.value = false
    return
  }
  await load()
})

onBeforeUnmount(() => {
  if (savedTimer) clearTimeout(savedTimer)
})
</script>

<template>
  <main class="settings" data-surface="tool">
    <header class="bar">
      <NuxtLink to="/me" class="back" aria-label="返回我的页面">←</NuxtLink>
      <h1>设置</h1>
      <span v-if="saved" class="saved" role="status">{{ saved }}</span>
      <span v-else-if="saving" class="saved">保存中…</span>
    </header>

    <p v-if="message" class="notice" role="alert">{{ message }}</p>
    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后可以调整通知、隐私与播放偏好。</p>
      <button type="button" class="cta" @click="openAuthDialog()">登录</button>
    </section>

    <VpAsyncState
      v-else :state="phase" :error="errorText" :retrying="retrying"
      variant="panel" @retry="load()"
    >
      <template v-if="settings">
      <section class="group">
        <h2>账号</h2>
        <div class="row">
          <div class="label">
            <strong>头像</strong>
            <small>PNG / JPEG / WebP，不超过 2 MiB</small>
          </div>
          <div class="control avatar-control">
            <span class="avatar-thumb">
              <img v-if="avatarPreview || avatarUrl" :src="avatarPreview || avatarUrl" alt="当前头像" />
              <span v-else class="avatar-fallback">{{ (displayName || profile?.handle || '我').slice(0, 1) }}</span>
            </span>
            <label class="avatar-btn">
              {{ avatarBusy ? '处理中…' : '更换头像' }}
              <input
                type="file" accept="image/png,image/jpeg,image/webp" hidden
                :disabled="avatarBusy || loading" @change="onAvatarPick"
              />
            </label>
            <button
              v-if="!avatarBusy" type="button" class="avatar-btn ghost"
              @click="openRandomAvatar"
            >随机头像</button>
            <button
              v-if="avatarUrl && !avatarBusy" type="button" class="avatar-btn ghost"
              @click="avatarConfirming = true"
            >移除</button>
          </div>
        </div>
        <div class="row">
          <div class="label">
            <strong>昵称</strong>
            <small>展示在作品与作者主页，1–24 个字符</small>
          </div>
          <div class="control input">
            <input v-model="displayName" class="text" maxlength="24" aria-label="昵称" />
          </div>
        </div>
        <div class="row stacked">
          <div class="label">
            <strong>简介</strong>
            <small>最多 120 个字符，留空则不展示</small>
          </div>
          <textarea v-model="bio" class="text area" rows="3" maxlength="120" aria-label="简介" />
        </div>
        <div class="row">
          <div class="label"><strong>账号</strong></div>
          <span class="value">夜街号：{{ profile?.handle }}</span>
        </div>
        <div class="row">
          <div class="label">
            <strong>邮箱</strong>
            <small>用于登录，暂不支持修改</small>
          </div>
          <span class="value">{{ profile?.email }}</span>
        </div>
        <div class="row">
          <div class="label">
            <strong>密码</strong>
            <small>建议定期更换，至少 8 个字符</small>
          </div>
          <div class="control">
            <button type="button" class="btn" @click="openPassword">修改密码</button>
          </div>
        </div>
        <div class="foot">
          <button type="button" class="btn primary" :disabled="profileSaving" @click="saveProfile">
            {{ profileSaving ? '保存中…' : '保存资料' }}
          </button>
        </div>
      </section>

      <section class="group">
        <h2>隐私</h2>
        <div class="row">
          <div class="label">
            <strong>新作品默认可见性</strong>
            <small>发布页以此为初值，单条作品仍可修改</small>
          </div>
          <div class="control select">
            <VpSelect
              :model-value="settings.default_visibility"
              :options="visibilityOptions"
              variant="field"
              aria-label="新作品默认可见性"
              @change="onVisibilityChange"
            />
          </div>
        </div>
      </section>

      <section class="group">
        <h2>通知</h2>
        <div class="row">
          <div class="label">
            <strong>互动通知</strong>
            <small>收到点赞、收藏与评论时提醒</small>
          </div>
          <div class="control switch">
            <VpSwitch
              :model-value="settings.notify_interaction"
              aria-label="互动通知"
              @update:model-value="save({ notify_interaction: $event })"
            />
          </div>
        </div>
        <div class="row">
          <div class="label">
            <strong>关注通知</strong>
            <small>有人关注你时提醒</small>
          </div>
          <div class="control switch">
            <VpSwitch
              :model-value="settings.notify_follow"
              aria-label="关注通知"
              @update:model-value="save({ notify_follow: $event })"
            />
          </div>
        </div>
        <div class="row">
          <div class="label">
            <strong>系统通知</strong>
            <small>审核结论、申诉结果、举报反馈与账号处置</small>
          </div>
          <div class="control switch">
            <VpSwitch
              :model-value="settings.notify_system"
              aria-label="系统通知"
              @update:model-value="save({ notify_system: $event })"
            />
          </div>
        </div>
        <div class="row">
          <div class="label">
            <strong>免打扰</strong>
            <small>时段内不产生互动与关注通知；审核、申诉与举报结果始终送达</small>
          </div>
          <div class="control switch">
            <VpSwitch :model-value="quietOn" aria-label="免打扰" @update:model-value="toggleQuiet" />
          </div>
        </div>
        <div v-if="quietOn" class="row">
          <div class="label"><strong>时段</strong></div>
          <div class="control range">
            <VpSelect
              :model-value="quietStart" :options="timeOptions" aria-label="免打扰开始时间"
              @change="onQuietStartChange"
            />
            <span class="dash">至</span>
            <VpSelect
              :model-value="quietEnd" :options="timeOptions" aria-label="免打扰结束时间"
              @change="onQuietEndChange"
            />
          </div>
        </div>
      </section>

      <section class="group">
        <h2>播放与材质</h2>
        <div class="row">
          <div class="label">
            <strong>滚动自动播放</strong>
            <small>关闭后滑到新作品不自动播放，需点按开始</small>
          </div>
          <div class="control switch">
            <VpSwitch
              :model-value="settings.autoplay"
              aria-label="滚动自动播放"
              @update:model-value="save({ autoplay: $event })"
            />
          </div>
        </div>
        <div class="row">
          <div class="label">
            <strong>减少动效</strong>
            <small>压掉界面过渡与动画，适合对动效敏感的使用场景</small>
          </div>
          <div class="control switch">
            <VpSwitch
              :model-value="settings.reduce_motion"
              aria-label="减少动效"
              @update:model-value="save({ reduce_motion: $event })"
            />
          </div>
        </div>
      </section>

      <section class="group">
        <h2>通用</h2>
        <div class="row">
          <div class="label">
            <strong>工具页主题</strong>
            <small>仅影响设置与创作者中心；沉浸观看界面恒为深色</small>
          </div>
          <div class="control theme">
            <div class="theme-picker" role="radiogroup" aria-label="工具页主题">
              <button
                v-for="item in themeOptions" :key="item.value" type="button"
                class="theme-option" role="radio"
                :class="{ on: currentTheme === item.value }"
                :aria-checked="currentTheme === item.value"
                @click="onThemeChange(item.value)"
              >{{ item.label }}</button>
            </div>
          </div>
        </div>
        <div class="row theme-status">
          <div class="label">
            <strong>当前：{{ themeResolved }}</strong>
            <small>{{ themeHint }}</small>
          </div>
        </div>
      </section>
      </template>
    </VpAsyncState>

    <!-- 版本号：排障/反馈时报这个，对应 CHANGELOG.md 里的同名版本条目。 -->
    <p class="version-foot">夜街 v{{ appVersion }}</p>

    <VpDialog
      :open="randomAvatarOpen" title="选一张随机头像" :busy="randomAvatarBusy"
      @close="randomAvatarOpen = false"
    >
      <AvatarPicker :selected="pickedSeed" @select="pickedSeed = $event" />
      <div class="dialog-actions">
        <button type="button" class="btn" @click="randomAvatarOpen = false">取消</button>
        <button
          type="button" class="btn primary"
          :disabled="!pickedSeed || randomAvatarBusy" @click="applyRandomAvatar"
        >{{ randomAvatarBusy ? '保存中…' : '使用这张' }}</button>
      </div>
    </VpDialog>

    <VpDialog :open="pwd.open" title="修改密码" :busy="pwd.busy" @close="pwd.open = false">
        <label class="field">
          <span>当前密码</span>
          <input v-model="pwd.current" type="password" autocomplete="current-password" class="text" />
        </label>
        <label class="field">
          <span>新密码</span>
          <input v-model="pwd.next" type="password" autocomplete="new-password" class="text" />
        </label>
        <label class="field">
          <span>确认新密码</span>
          <input v-model="pwd.confirm" type="password" autocomplete="new-password" class="text" />
        </label>
        <p v-if="pwd.error" class="err" role="alert">{{ pwd.error }}</p>
        <div class="dialog-actions">
          <button type="button" class="btn" @click="pwd.open = false">取消</button>
          <button type="button" class="btn primary" :disabled="pwd.busy" @click="changePassword">
            {{ pwd.busy ? '提交中…' : '确认修改' }}
          </button>
        </div>
    </VpDialog>

    <VpConfirmDialog
      :open="avatarConfirming" title="移除头像" :busy="avatarBusy"
      confirm-text="移除" busy-text="移除中…"
      @close="avatarConfirming = false" @confirm="removeAvatar"
    >
      确定移除当前头像吗？移除后恢复系统默认头像。
    </VpConfirmDialog>
  </main>
</template>

<style scoped>
.settings {
  min-height: 100%;
  padding: 18px 18px calc(40px + env(safe-area-inset-bottom));
  box-sizing: border-box;
  color: var(--vp-tool-text);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: 720px; margin: 0 auto 16px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid var(--vp-tool-border-strong);
  color: var(--vp-tool-text); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.saved { margin-left: auto; font-size: 12px; color: var(--vp-tool-accent-ink); }

.hint, .gate { max-width: 720px; margin: 40px auto; text-align: center; color: var(--vp-tool-text-2); font-size: 13px; }
.gate .cta {
  display: inline-block; margin-top: 12px; padding: 9px 20px; border-radius: 99px; border: 0; cursor: pointer; font-family: inherit;
  background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); text-decoration: none; font-size: 13px;
}
.notice {
  max-width: 720px; margin: 0 auto 12px; padding: 10px 12px; border-radius: 10px;
  background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink); font-size: 12px;
}

/* 版本号脚注：弱化到不抢注意力，但足够可读以便报障时报出。 */
.version-foot {
  max-width: 720px; margin: 20px auto 8px; text-align: center;
  font-size: 11px; letter-spacing: 0.02em; color: var(--vp-tool-text-2);
}

.group {
  max-width: 720px; margin: 0 auto 14px; padding: 6px 16px 4px;
  border-radius: 16px;
  background: var(--vp-tool-surface);
  border: 1px solid var(--vp-tool-border);
}
.group h2 {
  margin: 12px 0 4px; font-size: 12px; font-weight: 600;
  letter-spacing: 0.06em; color: var(--vp-tool-text-2);
}
.row {
  display: flex; align-items: center; gap: 12px;
  min-height: 54px; padding: 8px 0;
  border-top: 1px solid var(--vp-tool-divider);
}
/* 控件统一放进 .control：直接作为 flex 子项时，VpSelect 的 width:100%
   会把同行的标签挤成 0 宽（手机端表现为一字一行），输入框也会顶出视口。 */
.control { flex: none; margin-left: auto; min-width: 0; }
.control.input { width: 190px; }
.control.select { width: 172px; }
.control.range { display: flex; align-items: center; gap: 8px; }
.control.range :deep(.vp-select) { width: 86px; }
.avatar-control { display: flex; align-items: center; gap: 10px; }
.avatar-thumb {
  width: 44px; height: 44px; flex: none; border-radius: 50%; overflow: hidden;
  display: grid; place-items: center;
  background: var(--vp-tool-surface-2); border: 1px solid var(--vp-tool-border);
}
.avatar-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
.avatar-fallback { font-size: 15px; color: var(--vp-tool-text-2); }
.avatar-btn {
  padding: 6px 12px; border-radius: 99px; cursor: pointer; font-size: 11px;
  border: 1px solid var(--vp-tool-border-strong); background: transparent;
  color: var(--vp-tool-text);
}
.avatar-btn.ghost { color: var(--vp-tool-text-2); }
.avatar-btn:disabled { opacity: .6; cursor: not-allowed; }
.control.input .text { width: 100%; box-sizing: border-box; }

/* §14 主题三选：选中态不只靠颜色（加粗 + 底色 + aria-checked）。 */
.theme-picker {
  display: inline-flex; gap: 4px; padding: 3px; border-radius: 99px;
  background: var(--vp-tool-surface-2);
}
.theme-option {
  padding: 5px 13px; border: 0; border-radius: 99px; cursor: pointer;
  background: transparent; color: var(--vp-tool-text-2); font-size: 12px;
  font-family: inherit; white-space: nowrap;
}
.theme-option.on {
  background: var(--vp-tool-accent-soft); color: var(--vp-tool-accent-ink); font-weight: 600;
}
.theme-status { border-top: 1px solid var(--vp-tool-divider); }
.theme-status .label strong { font-size: 13px; font-weight: 500; }
.group h2 + .row { border-top: 0; }
.row.stacked { display: block; }
.label { flex: 1; min-width: 0; display: grid; gap: 3px; }
.label strong { font-size: 14px; font-weight: 500; }
.label small { font-size: 11px; line-height: 1.5; color: var(--vp-tool-text-2); }
.label small a { color: var(--vp-tool-accent-ink); text-decoration: underline; }
.value {
  font-size: 13px; color: var(--vp-tool-text-2);
  min-width: 0; text-align: right; word-break: break-all;
}
.row.stacked .label { margin-bottom: 8px; }

.text {
  min-width: 0; padding: 9px 11px; border-radius: 9px;
  border: 1px solid var(--vp-tool-input-border);
  background: var(--vp-tool-input);
  color: var(--vp-tool-text); font-size: 13px; font-family: inherit;
}
input.text { width: 200px; }
.area { width: 100%; resize: vertical; box-sizing: border-box; }
.text:focus-visible {
  outline: none; border-color: var(--vp-tool-accent-ink);
  box-shadow: 0 0 0 3px var(--vp-tool-focus);
}

.range { display: flex; align-items: center; gap: 8px; }
.dash { font-size: 12px; color: var(--vp-tool-text-2); }

.btn {
  padding: 8px 14px; border-radius: 99px; cursor: pointer;
  border: 1px solid var(--vp-tool-border-strong);
  background: transparent; color: var(--vp-tool-text); font-size: 12px;
}
.btn.primary { border-color: transparent; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); font-weight: 600; }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
.foot { display: flex; justify-content: flex-end; padding: 10px 0 14px; }

.field { display: grid; gap: 6px; margin-bottom: 10px; }
.field span { font-size: 12px; color: var(--vp-tool-text-2); }
.field .text { width: 100%; box-sizing: border-box; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 14px; }
.err { margin: 4px 0 0; font-size: 12px; color: var(--vp-tool-danger-ink); }

@media (max-width: 560px) {
  .row { flex-wrap: wrap; }
  .control.input, .control.select, .control.range, .control.theme {
    flex: 1 1 100%; width: 100%; margin-left: 0;
  }
  .control.range :deep(.vp-select) { flex: 1; width: auto; }
  .theme-picker { display: flex; }
  .theme-option { flex: 1; text-align: center; }
  .value { text-align: left; }
}
</style>
