<script setup lang="ts">
// 「我」：作品列表与审核结论入口（§9.4 驳回/下架可在站内申诉）。
// 私有页面：动态 SSR、禁止公共缓存、不参与索引。
import { ApiError, errorMessage } from '~/composables/useApi'

useSeoMeta({ title: '我 · 夜街', robots: 'noindex,nofollow' })

interface ContentItem {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  description: string
  publish_state: string
  review_status: string
  media_status?: string
  visibility: string
  created_at: string
}

interface AppealItem {
  id: number
  content_id: number
  status: 'pending' | 'accepted' | 'rejected'
  decision_note?: string
}

interface ReactedItem {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  author_handle: string
  created_at: string
}

interface MyProfile {
  handle: string
  display_name: string
  bio: string
  avatar_url?: string
}

interface MyReport {
  id: number
  object_type: string
  object_id: number
  object_title?: string
  reason: string
  detail: string
  status: string
  case_status?: string
  resolution?: string
  created_at: string
}

const { api, getMe } = useApi()
// 三个 tab 各有自己的空态文案，状态机只接管 加载 / 错误 / 离线 / 权限不足。
const asyncState = useAsyncState({ keepOnError: true })
const { phase, errorText, partial, retrying } = asyncState

type ProfileTab = 'works' | 'likes' | 'favorites'

const items = ref<ContentItem[]>([])
const tab = ref<ProfileTab>('works')
const counts = ref<{ works: number; likes_received: number; following: number; followers: number } | null>(null)
// 头部本人信息（§9.1 / §11.1）：头像、昵称、账号标识、编辑资料入口。
const profile = ref<MyProfile | null>(null)
const reacted = ref<Record<'likes' | 'favorites', ReactedItem[]>>({ likes: [], favorites: [] })
const reactedLoaded = reactive<Record<'likes' | 'favorites', boolean>>({ likes: false, favorites: false })
const reactedLoading = ref(false)
const appeals = ref<Record<number, AppealItem>>({})
const reports = ref<MyReport[]>([])
const loading = ref(false)
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')
const dialog = reactive({
  open: false,
  item: null as ContentItem | null,
  reason: '',
  pending: false,
  error: '',
})

const statusText: Record<string, string> = {
  pending: '待审核', approved: '已通过', rejected: '已驳回',
  taken_down: '已下架', limited: '已限流',
}
// 媒体状态徽标（§10）：视频未出可播产物时显示「处理中 / 处理失败」。
const mediaText: Record<string, string> = {
  processing: '处理中', failed: '处理失败', none: '处理失败',
}
const reportStatusText: Record<string, string> = {
  pending: '待处理', upheld: '已处置成立', dismissed: '已处置不成立',
}
const appealable = new Set(['rejected', 'taken_down', 'limited'])

async function load() {
  loading.value = true
  message.value = ''
  asyncState.begin()
  try {
    const [works, mine, reported, stats, owner] = await Promise.all([
      api<{ items: ContentItem[] }>('/api/v1/contents/mine?limit=50'),
      api<{ items: AppealItem[] }>('/api/v1/appeals/mine'),
      api<{ items: MyReport[] }>('/api/v1/reports/mine').catch(() => ({ items: [] })),
      api<{ works: number; likes_received: number; following: number; followers: number }>(
        '/api/v1/me/counts').catch(() => null),
      api<MyProfile>('/api/v1/me/profile').catch(() => null),
    ])
    counts.value = stats
    profile.value = owner
    items.value = works.items || []
    appeals.value = Object.fromEntries((mine.items || []).map((item) => [item.content_id, item]))
    reports.value = reported.items || []
    asyncState.succeed()
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
      asyncState.succeed()
    } else {
      asyncState.fail(error)
    }
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
    return
  }
  await load()
})

function appealLabel(item: ContentItem): string {
  const record = appeals.value[item.id]
  if (!record) return ''
  if (record.status === 'pending') return '申诉处理中'
  if (record.status === 'accepted') return '申诉已通过'
  return '申诉未通过'
}

function canAppeal(item: ContentItem): boolean {
  if (!appealable.has(item.review_status)) return false
  return appeals.value[item.id]?.status !== 'pending'
}

function openAppeal(item: ContentItem) {
  dialog.open = true
  dialog.item = item
  dialog.reason = ''
  dialog.error = ''
}

// 喜欢 / 收藏按需加载，切回来不重复请求。
async function switchTab(next: ProfileTab) {
  tab.value = next
  if (next === 'works' || reactedLoaded[next]) return
  reactedLoading.value = true
  try {
    const data = await api<{ items: ReactedItem[] }>(`/api/v1/me/${next}?limit=50`)
    reacted.value[next] = data.items || []
    reactedLoaded[next] = true
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    reactedLoading.value = false
  }
}

async function submitAppeal() {
  if (!dialog.item) return
  const reason = dialog.reason.trim()
  if (reason.length < 5) {
    dialog.error = '请填写至少 5 个字符的申诉理由'
    return
  }
  dialog.pending = true
  dialog.error = ''
  try {
    await api(`/api/v1/contents/${dialog.item.id}/appeal`, {
      method: 'POST', body: { reason },
    })
    dialog.open = false
    await load()
  } catch (error) {
    dialog.error = errorMessage(error)
  } finally {
    dialog.pending = false
  }
}
</script>

<template>
  <main class="me" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/" class="back" aria-label="回到沉浸流">←</NuxtLink>
      <h1>我</h1>
      <NuxtLink to="/studio" class="link">创作中心</NuxtLink>
      <NuxtLink to="/library" class="link">个人库</NuxtLink>
      <NuxtLink to="/me/drafts" class="link">草稿箱</NuxtLink>
      <NuxtLink to="/settings" class="link">设置</NuxtLink>
      <NuxtLink to="/messages" class="link">消息</NuxtLink>
      <NuxtLink to="/publish" class="link primary">发布作品</NuxtLink>
    </header>

    <p v-if="message" class="notice" role="status">{{ message }}</p>
    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后管理自己的作品、查看审核结论与申诉进度。</p>
      <NuxtLink to="/login?redirect=%2Fme" class="cta">登录</NuxtLink>
    </section>

    <VpAsyncState
      v-else :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="3" @retry="load()"
    >
      <section v-if="profile" class="profile-head" aria-label="本人资料">
        <span class="avatar">
          <img
            v-if="profile.avatar_url"
            :src="profile.avatar_url" :alt="`${profile.display_name || profile.handle} 的头像`"
          />
          <span v-else>{{ (profile.display_name || profile.handle).slice(0, 1) }}</span>
        </span>
        <div class="who">
          <strong>{{ profile.display_name || profile.handle }}</strong>
          <span>@{{ profile.handle }}</span>
          <p v-if="profile.bio">{{ profile.bio }}</p>
        </div>
        <NuxtLink to="/settings" class="edit">编辑资料</NuxtLink>
      </section>

      <section v-if="counts" class="counts" aria-label="账号数据">
        <NuxtLink to="/me/relations?tab=likes">
          <strong>{{ counts.likes_received }}</strong><span>获赞</span>
        </NuxtLink>
        <NuxtLink to="/me/relations?tab=following">
          <strong>{{ counts.following }}</strong><span>关注</span>
        </NuxtLink>
        <NuxtLink to="/me/relations?tab=followers">
          <strong>{{ counts.followers }}</strong><span>粉丝</span>
        </NuxtLink>
      </section>

      <nav class="tabs" aria-label="作品分区">
        <button type="button" :class="{ on: tab === 'works' }" @click="switchTab('works')">作品</button>
        <button type="button" :class="{ on: tab === 'likes' }" @click="switchTab('likes')">喜欢</button>
        <button type="button" :class="{ on: tab === 'favorites' }" @click="switchTab('favorites')">收藏</button>
      </nav>

      <ul v-if="tab === 'works' && items.length" class="list">
        <li v-for="item in items" :key="item.id">
          <div class="row">
            <div class="meta">
              <strong>{{ item.title || '（无标题）' }}</strong>
              <span class="sub">
                {{ item.type === 'image' ? '图集' : '视频' }} ·
                {{ item.publish_state === 'draft' ? '草稿' : item.publish_state === 'scheduled' ? '定时发布' : '已发布' }}
                · {{ item.visibility === 'public' ? '公开' : item.visibility === 'friends' ? '好友' : '私密' }}
              </span>
            </div>
            <span
              v-if="item.type === 'video' && item.publish_state !== 'draft' && mediaText[item.media_status || '']"
              class="status media"
            >{{ mediaText[item.media_status!] }}</span>
            <span class="status" :class="item.review_status">
              {{ statusText[item.review_status] || item.review_status }}
            </span>
            <span v-if="appealLabel(item)" class="appeal-state" :class="appeals[item.id]?.status">
              {{ appealLabel(item) }}
            </span>
            <button
              v-if="canAppeal(item)" type="button" class="appeal"
              @click="openAppeal(item)"
            >{{ appeals[item.id] ? '再次申诉' : '申诉' }}</button>
          </div>
          <p v-if="appeals[item.id]?.decision_note" class="note">
            处理说明：{{ appeals[item.id]?.decision_note }}
          </p>
        </li>
      </ul>
      <p v-else-if="tab === 'works' && !loading" class="hint">还没有作品，去发布第一条吧。</p>

      <template v-if="tab !== 'works'">
        <div v-if="reactedLoading" class="hint">加载中…</div>
        <ul v-else-if="reacted[tab].length" class="list">
          <li v-for="item in reacted[tab]" :key="item.id">
            <NuxtLink class="reacted" :to="`/${item.type}/${item.slug}`">
              <div class="meta">
                <strong>{{ item.title || '（无标题）' }}</strong>
                <span class="sub">
                  {{ item.type === 'image' ? '图集' : '视频' }} · @{{ item.author_handle }}
                </span>
              </div>
              <span class="chev" aria-hidden="true">›</span>
            </NuxtLink>
          </li>
        </ul>
        <p v-else class="hint">
          {{ tab === 'likes' ? '还没有喜欢的作品，去沉浸流里点点看。' : '还没有收藏的作品，看到喜欢的点右侧收藏。' }}
        </p>
      </template>

      <section v-if="reports.length" class="reports">
        <h2>我的举报</h2>
        <ul>
          <li v-for="report in reports" :key="report.id">
            <div class="report-head">
              <strong>{{ report.object_title || ('# ' + report.object_id) }}</strong>
              <span class="status" :class="report.status">
                {{ reportStatusText[report.status] || report.status }}
              </span>
            </div>
            <p class="note">理由：{{ report.reason }}<template v-if="report.detail"> · {{ report.detail }}</template></p>
            <p v-if="report.resolution" class="note">处理结果：{{ report.resolution }}</p>
          </li>
        </ul>
      </section>
    </VpAsyncState>

    <VpDialog :open="dialog.open" title="提交申诉" :busy="dialog.pending" @close="dialog.open = false">
        <p class="tip">
          作品《{{ dialog.item?.title || '（无标题）' }}》当前为
          {{ statusText[dialog.item?.review_status || ''] || dialog.item?.review_status }}。
          请说明你认为处理有误的具体原因（5–500 字），审核员会重新核对。
        </p>
        <textarea v-model="dialog.reason" rows="5" maxlength="500" placeholder="申诉理由" />
        <p v-if="dialog.error" class="error">{{ dialog.error }}</p>
        <div class="actions">
          <button type="button" class="ghost" @click="dialog.open = false">取消</button>
          <button type="button" class="primary-btn" :disabled="dialog.pending" @click="submitAppeal">
            {{ dialog.pending ? '提交中…' : '提交申诉' }}
          </button>
        </div>
    </VpDialog>
  </main>
</template>

<style scoped>
.me {
  min-height: 100dvh;
  padding: 20px 20px 56px;
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: 820px; margin: 0 auto 18px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.link {
  margin-left: 8px; padding: 6px 11px; border-radius: 99px; font-size: 12px;
  white-space: nowrap;
  border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.link.primary { margin-left: auto; border-color: rgba(255, 46, 126, 0.5); }
.list { list-style: none; margin: 0 auto; padding: 0; max-width: 820px; display: grid; gap: 10px; }
.list li {
  padding: 14px 16px; border-radius: 14px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.06);
}
.row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.meta strong { font-size: 13.5px; }
.sub { display: block; margin-top: 3px; font-size: 11px; color: var(--vp-im-text-secondary); }
.status {
  padding: 2px 9px; border-radius: 99px; font-size: 11px;
  background: rgba(255, 255, 255, 0.08); color: var(--vp-im-text-secondary);
}
.status.rejected, .status.taken_down, .status.limited {
  background: rgba(251, 113, 133, 0.14); color: #fb7185;
}
.status.approved { background: rgba(52, 211, 153, 0.14); color: #34d399; }
.appeal-state { font-size: 11px; color: var(--vp-im-text-secondary); }
.appeal-state.pending { color: #fbbf24; }
.appeal-state.accepted { color: #34d399; }
.appeal-state.rejected { color: #fb7185; }
.appeal {
  margin-left: auto; padding: 5px 13px; border-radius: 99px; cursor: pointer;
  border: 1px solid rgba(255, 46, 126, 0.5); background: transparent;
  color: var(--vp-im-text-primary); font-size: 12px;
}
.profile-head {
  max-width: 820px; margin: 4px auto 14px;
  display: flex; align-items: center; gap: 14px;
}
.profile-head .avatar {
  width: 56px; height: 56px; flex: none; border-radius: 50%; overflow: hidden;
  display: grid; place-items: center;
  background: var(--vp-im-surface-raised); border: 1px solid var(--vp-im-border);
  font-size: 20px; color: var(--vp-im-text-secondary);
}
.profile-head .avatar img { width: 100%; height: 100%; object-fit: cover; display: block; }
.profile-head .who { min-width: 0; display: grid; gap: 2px; }
.profile-head .who strong { font-size: 16px; color: var(--vp-im-text-primary); }
.profile-head .who span { font-size: 12px; color: var(--vp-im-text-secondary); }
.profile-head .who p {
  margin: 2px 0 0; font-size: 12px; line-height: 1.6;
  color: var(--vp-im-text-secondary); overflow-wrap: anywhere;
}
.profile-head .edit {
  margin-left: auto; flex: none; padding: 6px 14px; border-radius: 99px;
  font-size: 12px; text-decoration: none;
  border: 1px solid var(--vp-im-border); color: var(--vp-im-text-primary);
}
.counts {
  max-width: 820px; margin: 4px auto 14px; display: flex; gap: 28px;
}
.counts a {
  display: grid; gap: 2px; text-align: center;
  color: inherit; text-decoration: none;
}
.counts a:hover strong, .counts a:focus-visible strong { color: var(--vp-im-accent); }
.counts a:focus-visible { outline: none; }
.counts strong { font-size: 18px; font-weight: 600; color: var(--vp-im-text-primary); }
.counts span { font-size: 11px; color: var(--vp-im-text-secondary); }
.tabs {
  max-width: 820px; margin: 0 auto 12px; display: flex; gap: 6px;
}
.tabs button {
  padding: 6px 14px; border: 0; border-radius: 99px; cursor: pointer;
  background: rgba(255, 255, 255, 0.06); color: var(--vp-im-text-secondary);
  font-size: 12px;
}
.tabs button.on { background: rgba(255, 46, 126, 0.16); color: var(--vp-im-accent); font-weight: 600; }
.reacted { display: flex; align-items: center; gap: 10px; text-decoration: none; color: inherit; }
.reacted .meta { flex: 1; min-width: 0; }
.reacted .chev { color: var(--vp-im-text-secondary); font-size: 16px; }
.note { margin: 8px 0 0; font-size: 12px; line-height: 1.6; color: var(--vp-im-text-secondary); }
.reports { max-width: 820px; margin: 26px auto 0; }
.reports h2 { margin: 0 0 10px; font-size: 14px; color: var(--vp-im-text-secondary); }
.reports ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
.reports li {
  padding: 12px 14px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.06);
}
.report-head { display: flex; align-items: center; gap: 8px; }
.report-head strong { font-size: 13px; }
.report-head .status { margin-left: auto; }
.status.upheld { background: rgba(52, 211, 153, 0.14); color: #34d399; }
.status.dismissed { background: rgba(255, 255, 255, 0.08); }
.gate { max-width: 820px; margin: 40px auto; text-align: center; }
.gate p { margin: 0 0 14px; font-size: 13px; color: var(--vp-im-text-secondary); }
.cta {
  display: inline-block; padding: 8px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: #fff; font-size: 13px; text-decoration: none;
}
.notice { max-width: 820px; margin: 0 auto 12px; font-size: 12px; color: var(--vp-im-accent); }
.hint { max-width: 820px; margin: 24px auto; font-size: 13px; color: var(--vp-im-text-secondary); }
/* 弹窗壳由 VpDialog 提供；这里只保留表单内部样式，全部消费语义令牌。 */
.tip { margin: 0 0 12px; font-size: 12px; line-height: 1.7; color: var(--vp-tool-text-2); }
textarea {
  width: 100%; box-sizing: border-box; padding: 10px 12px; border-radius: 10px; resize: vertical;
  border: 1px solid var(--vp-tool-input-border);
  background: var(--vp-tool-input); color: var(--vp-tool-text);
  font-size: 13px; line-height: 1.6; font-family: inherit;
}
.error { margin: 8px 0 0; font-size: 12px; color: var(--vp-tool-danger-ink); }
.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; }
.ghost, .primary-btn {
  padding: 8px 16px; border-radius: 99px; font-size: 13px; cursor: pointer;
  border: 1px solid var(--vp-tool-border-strong); background: transparent;
  color: var(--vp-tool-text);
}
.primary-btn {
  border: 0; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent);
}
.primary-btn:disabled { opacity: 0.6; cursor: not-allowed; }
</style>
