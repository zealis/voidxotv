<script setup lang="ts">
import {
  ElButton, ElEmpty, ElMessage, ElSkeleton, ElSkeletonItem, ElTable, ElTableColumn,
} from 'element-plus'
import type {
  AdminHotItem, AdminOverview, AdminReviewItem, AdminTrendPoint,
} from '~/composables/useAdminApi'

const emit = defineEmits<{ 'open-review': [contentId: number] }>()
const { request, can } = useAdminApi()

const loading = ref(true)
const error = ref('')
const overview = ref<AdminOverview | null>(null)
const trend = ref<AdminTrendPoint[]>([])
const hot = ref<AdminHotItem[]>([])
const pending = ref<AdminReviewItem[]>([])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [overviewData, trendData, hotData, reviewData] = await Promise.all([
      request<AdminOverview>('/api/v1/admin/stats/overview'),
      request<{ items: AdminTrendPoint[] }>('/api/v1/admin/stats/trend?days=7'),
      request<{ items: AdminHotItem[] }>('/api/v1/admin/stats/hot?limit=8'),
      request<{ items: AdminReviewItem[] }>('/api/v1/admin/reviews?limit=5'),
    ])
    overview.value = overviewData
    trend.value = trendData.items || []
    hot.value = hotData.items || []
    pending.value = reviewData.items || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

// 重算榜单属于敏感操作：服务端要求二次确认，request() 会自动弹密码框并重试。
const aggregating = ref(false)
async function reaggregate() {
  if (aggregating.value) return
  aggregating.value = true
  try {
    await request('/api/v1/admin/stats/aggregate', { method: 'POST', body: {}, csrf: true })
    ElMessage.success('榜单已重算')
    await load()
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : '重算失败')
  } finally {
    aggregating.value = false
  }
}

function formatCount(value: number | null | undefined): string {
  const number = Number(value || 0)
  if (number >= 1e8) return `${(number / 1e8).toFixed(2)} 亿`
  if (number >= 1e4) return `${(number / 1e4).toFixed(1)} 万`
  return new Intl.NumberFormat('zh-CN').format(number)
}

function formatPercent(value: number | null | undefined): string {
  return `${Number(value || 0).toFixed(1)}%`
}

function formatTime(value: string | null | undefined): string {
  if (!value) return '—'
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

function waitLabel(minutes: number): string {
  if (minutes < 60) return `${minutes} 分钟`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时`
  return `${Math.floor(minutes / 1440)} 天`
}

const cards = computed(() => {
  const data = overview.value
  return [
    {
      key: 'plays', label: '总播放量', value: formatCount(data?.total_plays),
      hint: '有效播放 · 30 分钟去重', delta: data?.deltas?.plays,
    },
    {
      key: 'users', label: '新增用户', value: formatCount(data?.new_users),
      hint: '近 7 日累计', delta: data?.deltas?.new_users,
    },
    {
      key: 'pending', label: '待审内容', value: formatCount(data?.pending_reviews),
      hint: '当前存量（时点值）', delta: null,
    },
    {
      key: 'reports', label: '举报处理率', value: formatPercent(data?.report_resolution_rate),
      hint: '近 7 日已处置 / 新增', delta: data?.deltas?.report_resolution_rate,
    },
  ]
})
</script>

<template>
  <section class="overview">
    <div v-if="loading" class="skeleton-grid">
      <ElSkeleton v-for="index in 4" :key="index" animated>
        <template #template><ElSkeletonItem variant="rect" class="skeleton-card" /></template>
      </ElSkeleton>
    </div>

    <div v-else-if="error" class="state">
      <p>{{ error }}</p>
      <ElButton type="primary" @click="load">重试</ElButton>
    </div>

    <template v-else>
      <div class="meta">
        <span>{{ overview?.period_label || '近 7 日 · 每 5 分钟更新' }}</span>
        <span>最后更新 {{ formatTime(overview?.updated_at) }}</span>
      </div>

      <div v-if="overview && !overview.data_ready" class="state">
        <p>{{ overview.note }}</p>
        <ElButton type="primary" @click="load">重新检查</ElButton>
      </div>

      <div v-if="overview?.data_ready" class="cards">
        <article v-for="card in cards" :key="card.key" class="card">
          <header>
            <span>{{ card.label }}</span>
            <em v-if="card.delta" :class="['delta', card.delta.direction]">
              {{ card.delta.label }}
            </em>
          </header>
          <strong>{{ card.value }}</strong>
          <p>{{ card.hint }}</p>
        </article>
      </div>

      <TrendChart :points="trend" :loading="false" />

      <div class="split">
        <section class="panel">
          <header class="panel-head">
            <h3>待审核内容</h3>
            <span class="badge">{{ pending.length }} 条待处理</span>
          </header>
          <ElEmpty v-if="!pending.length" description="暂无待审内容" :image-size="72" />
          <ul v-else class="review-list">
            <li v-for="item in pending" :key="item.content_id">
              <img
                v-if="item.poster_url" :src="item.poster_url" :alt="item.title || '待审作品'"
                loading="lazy"
              >
              <div v-else class="thumb" aria-hidden="true">{{ item.type === 'image' ? '图' : '视' }}</div>
              <div class="review-body">
                <p class="review-title">{{ item.title || '（无标题）' }}</p>
                <p class="review-sub">
                  @{{ item.handle }} · 等待 {{ waitLabel(item.waiting_minutes) }}
                </p>
                <div class="tags">
                  <AdminStatusTag
                    v-for="label in item.risk_labels" :key="label"
                    :text="label" :tone="item.report_count >= 3 ? 'danger' : 'attention'"
                  />
                  <AdminStatusTag v-if="!item.risk_labels.length" text="常规队列" tone="neutral" />
                </div>
              </div>
              <ElButton size="small" type="primary" @click="emit('open-review', item.content_id)">
                审核
              </ElButton>
            </li>
          </ul>
        </section>

        <section class="panel">
          <header class="panel-head">
            <h3>热门作品 TOP</h3>
            <div class="panel-actions">
              <span class="badge">近 7 日 · 每小时重算</span>
              <ElButton
                v-if="can('stats:aggregate')" size="small" :loading="aggregating"
                @click="reaggregate"
              >重算</ElButton>
            </div>
          </header>
          <ElEmpty v-if="!hot.length" description="暂无榜单数据" :image-size="72" />
          <ElTable v-else :data="hot" size="small" class="admin-table hot-table">
            <ElTableColumn prop="rank" label="#" width="44" />
            <ElTableColumn label="作品" min-width="140">
              <template #default="scope">
                <div class="hot-title">{{ scope.row.title || '（无标题）' }}</div>
                <div class="hot-sub">@{{ scope.row.handle }}</div>
              </template>
            </ElTableColumn>
            <ElTableColumn label="播放" width="76">
              <template #default="scope">{{ formatCount(scope.row.plays) }}</template>
            </ElTableColumn>
            <ElTableColumn label="点赞" width="64">
              <template #default="scope">{{ formatCount(scope.row.likes) }}</template>
            </ElTableColumn>
            <ElTableColumn label="状态" width="82">
              <template #default="scope">
                <AdminStatusTag
                  :text="scope.row.status === 'approved' ? '已上架' : '审核中'"
                  :tone="scope.row.status === 'approved' ? 'positive' : 'attention'"
                />
              </template>
            </ElTableColumn>
          </ElTable>

          <!-- 窄屏卡片：榜单不再横向滚动，标题与计数完整可读。 -->
          <div v-if="hot.length" class="admin-cards">
            <article v-for="row in hot" :key="row.rank" class="admin-card">
              <header class="admin-card-head">
                <div>
                  <div class="admin-card-title">#{{ row.rank }} {{ row.title || '（无标题）' }}</div>
                  <div class="admin-card-sub">@{{ row.handle }}</div>
                </div>
                <div class="admin-card-tags">
                  <AdminStatusTag
                    :text="row.status === 'approved' ? '已上架' : '审核中'"
                    :tone="row.status === 'approved' ? 'positive' : 'attention'"
                  />
                </div>
              </header>
              <dl class="admin-card-meta">
                <div><dt>播放</dt><dd>{{ formatCount(row.plays) }}</dd></div>
                <div><dt>点赞</dt><dd>{{ formatCount(row.likes) }}</dd></div>
              </dl>
            </article>
          </div>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
.overview { display: grid; gap: 16px; }
.meta {
  display: flex; justify-content: space-between; gap: 12px;
  font-size: 12px; color: #8b8b93;
}
.cards {
  display: grid; gap: 12px;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
}
.card {
  padding: 16px 18px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07);
}
.card header {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 12px; color: #a1a1aa;
}
.card strong {
  display: block; margin: 8px 0 4px;
  font-size: 26px; font-weight: 700; letter-spacing: -0.02em; color: #fafafa;
}
.card p { margin: 0; font-size: 11px; color: #71717a; }
.delta { font-style: normal; font-size: 12px; font-weight: 600; }
.delta.up { color: #ff2e7e; }
.delta.down { color: #22d3ee; }
.delta.flat { color: #a1a1aa; }
.split {
  display: grid; gap: 16px;
  grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
}
@media (max-width: 1100px) { .split { grid-template-columns: 1fr; } }
.panel {
  padding: 16px 18px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07);
}
.panel-head {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: 10px;
}
.panel-actions { display: flex; align-items: center; gap: 10px; }
.panel-head h3 { margin: 0; font-size: 14px; font-weight: 600; color: #f4f4f5; }
.badge {
  padding: 2px 8px; border-radius: 99px; font-size: 11px;
  background: rgba(255, 46, 126, 0.14); color: #ff8ab5;
}
.review-list { list-style: none; margin: 0; padding: 0; display: grid; gap: 10px; }
.review-list li {
  display: grid; grid-template-columns: 64px minmax(0, 1fr) auto;
  gap: 12px; align-items: center;
  padding: 10px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.02);
}
.review-list img, .thumb {
  width: 64px; height: 64px; border-radius: 10px; object-fit: cover;
  background: rgba(255, 255, 255, 0.06);
}
.thumb { display: grid; place-items: center; font-size: 12px; color: #a1a1aa; }
.review-title {
  margin: 0 0 2px; font-size: 13px; font-weight: 600; color: #f4f4f5;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.review-sub { margin: 0 0 6px; font-size: 11px; color: #8b8b93; }
.tags { display: flex; flex-wrap: wrap; gap: 4px; }
.hot-title { font-size: 12px; color: #f4f4f5; }
.hot-sub { font-size: 11px; color: #8b8b93; }
.hot-table { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; }
.skeleton-grid {
  display: grid; gap: 12px;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
}
.skeleton-card { height: 108px; border-radius: 16px; }
.state {
  display: grid; gap: 12px; place-items: start;
  padding: 24px; border-radius: 16px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07);
  font-size: 13px; color: #a1a1aa;
}
.state p { margin: 0; }
</style>
