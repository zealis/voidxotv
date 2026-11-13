<script setup lang="ts">
// 创作者中心 · 数据看板（UI 规范 §11.1 / §11.2：工具型页面，信息密度优先）。
// 数据口径：播放按天增量、点赞评论按事件时间、新增粉丝按关注时间，全部东八区自然日。

const { open: openAuthDialog } = useAuthDialog()

useSeoMeta({ title: '创作中心 · 夜街', robots: 'noindex,nofollow' })

interface Counts {
  plays: number
  likes: number
  comments: number
  followers: number
}

interface DayPoint {
  day: string
  plays: number
  likes: number
  comments: number
}

interface WorkRow {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  plays: number
  likes: number
  comments: number
}

interface Analytics {
  days: number
  start: string
  end: string
  totals: Counts
  previous: Counts
  series: DayPoint[]
  top: WorkRow[]
  lifetime: Counts
}

const { api, getMe } = useApi()
const route = useRoute()
const router = useRouter()

// 创作中心三块（§11.1）：数据看板 / 作品管理 / 评论管理，用 ?tab= 深链。
type StudioTab = 'overview' | 'works' | 'comments'
const studioTabs: { key: StudioTab; label: string }[] = [
  { key: 'overview', label: '数据看板' },
  { key: 'works', label: '作品管理' },
  { key: 'comments', label: '评论管理' },
]

function readTab(): StudioTab {
  const value = String(route.query.tab || '')
  return value === 'works' || value === 'comments' ? value : 'overview'
}

const tab = ref<StudioTab>(readTab())

// 看板即使本周期全 0 也有数据（有序列与累计值），所以不做 empty 态，
// 只由状态机处理 加载 / 错误 / 离线 / 权限不足 / 部分数据。
const asyncState = useAsyncState({ keepOnError: true })
const { phase, errorText, partial, retrying } = asyncState

const data = ref<Analytics | null>(null)
const days = ref<7 | 30>(7)
const loading = ref(false)
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')

function number(value: number): string {
  return value.toLocaleString('zh-CN')
}

function delta(current: number, previous: number): { text: string; up: boolean | null } {
  if (previous === 0) {
    return current > 0 ? { text: '新增', up: true } : { text: '—', up: null }
  }
  const rate = Math.round(((current - previous) / previous) * 100)
  if (rate === 0) return { text: '持平', up: null }
  // 基数很小时环比会飙到几千个百分点，展示上收敛，避免卡片被数字撑爆。
  if (rate > 999) return { text: '>+999%', up: true }
  if (rate < -999) return { text: '<-999%', up: false }
  return { text: `${rate > 0 ? '+' : ''}${rate}%`, up: rate > 0 }
}

const cards = computed(() => {
  if (!data.value) return []
  const { totals, previous } = data.value
  return [
    { key: 'plays', label: '播放', value: totals.plays, change: delta(totals.plays, previous.plays) },
    { key: 'likes', label: '点赞', value: totals.likes, change: delta(totals.likes, previous.likes) },
    { key: 'comments', label: '评论', value: totals.comments, change: delta(totals.comments, previous.comments) },
    { key: 'followers', label: '新增粉丝', value: totals.followers, change: delta(totals.followers, previous.followers) },
  ]
})

// 柱状图用固定 viewBox + 百分比宽度，窄屏也不会溢出。
const chart = computed(() => {
  const series = data.value?.series || []
  const width = 640
  const height = 150
  const gap = series.length > 14 ? 2 : 6
  const barWidth = series.length ? Math.max(2, (width - gap * (series.length - 1)) / series.length) : 0
  const max = Math.max(1, ...series.map((point) => point.plays))
  const bars = series.map((point, index) => {
    const barHeight = point.plays === 0 ? 0 : Math.max(3, (point.plays / max) * (height - 24))
    return {
      x: index * (barWidth + gap),
      y: height - 18 - barHeight,
      width: barWidth,
      height: barHeight,
      day: point.day,
      plays: point.plays,
      today: index === series.length - 1,
    }
  })
  return { width, height, bars, max }
})

function shortDay(day: string): string {
  const [, month, date] = day.split('-')
  return `${Number(month)}/${Number(date)}`
}

async function load() {
  loading.value = true
  message.value = ''
  await asyncState.run(async () => {
    const value = await api<Analytics>(`/api/v1/me/analytics?days=${days.value}`)
    data.value = value
    return value
  })
  loading.value = false
}

function switchTab(next: StudioTab) {
  if (next === tab.value) return
  tab.value = next
  void router.replace({ query: { ...route.query, tab: next } })
  // 看板数据只在第一次进入时拉取，切换 tab 不重复请求。
  if (next === 'overview' && !data.value) void load()
}

function switchDays(next: 7 | 30) {
  if (next === days.value) return
  days.value = next
  void load()
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
    return
  }
  if (tab.value === 'overview') await load()
})
</script>

<template>
  <main class="studio" data-surface="tool">
    <header class="bar">
      <NuxtLink to="/me" class="back" aria-label="返回我的页面">←</NuxtLink>
      <h1>创作中心</h1>
      <div v-if="tab === 'overview'" class="range" role="group" aria-label="统计周期">
        <button type="button" :class="{ on: days === 7 }" @click="switchDays(7)">7 天</button>
        <button type="button" :class="{ on: days === 30 }" @click="switchDays(30)">30 天</button>
      </div>
    </header>

    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后查看播放、点赞与粉丝变化。</p>
      <button type="button" class="cta" @click="openAuthDialog()">登录</button>
    </section>

    <template v-else>
      <nav class="studio-tabs" aria-label="创作中心分区">
        <button
          v-for="entry in studioTabs" :key="entry.key" type="button"
          :class="{ on: tab === entry.key }" :aria-current="tab === entry.key ? 'page' : undefined"
          @click="switchTab(entry.key)"
        >{{ entry.label }}</button>
      </nav>

      <template v-if="tab === 'overview'">
        <VpAsyncState
          :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
          variant="cards" @retry="load()"
        >
          <template v-if="data">
      <section class="cards" aria-label="周期数据">
        <article v-for="card in cards" :key="card.key" class="card">
          <span class="label">{{ card.label }}</span>
          <strong class="value">{{ number(card.value) }}</strong>
          <span class="change" :class="card.change.up === true ? 'up' : card.change.up === false ? 'down' : ''">
            {{ card.change.text }}
            <small>较上周期</small>
          </span>
        </article>
      </section>

      <section class="panel">
        <header class="panel-head">
          <h2>每日播放</h2>
          <span class="panel-meta">{{ shortDay(data.start) }} – {{ shortDay(data.end) }}</span>
        </header>

        <p v-if="!data.totals.plays && !data.totals.likes && !data.totals.comments" class="empty">
          这个周期还没有数据。发布作品并让更多人看到后，这里会出现趋势。
        </p>

        <svg
          v-else class="chart" :viewBox="`0 0 ${chart.width} ${chart.height}`"
          preserveAspectRatio="none" role="img"
          :aria-label="`每日播放趋势，峰值 ${chart.max} 次`"
        >
          <line
            v-for="line in [0, 1, 2]" :key="line"
            :x1="0" :x2="chart.width"
            :y1="18 + line * ((chart.height - 36) / 2)"
            :y2="18 + line * ((chart.height - 36) / 2)"
            class="grid"
          />
          <rect
            v-for="bar in chart.bars" :key="bar.day"
            :x="bar.x" :y="bar.y" :width="bar.width" :height="bar.height"
            :class="['bar', { today: bar.today }]"
          >
            <title>{{ bar.day }}：{{ bar.plays }} 次播放</title>
          </rect>
          <text :x="0" :y="chart.height - 4" class="axis">{{ shortDay(data.start) }}</text>
          <text :x="chart.width / 2" :y="chart.height - 4" class="axis" text-anchor="middle">
            {{ shortDay(data.series[Math.floor(data.series.length / 2)]?.day || data.end) }}
          </text>
          <text :x="chart.width" :y="chart.height - 4" class="axis" text-anchor="end">
            {{ shortDay(data.end) }}
          </text>
        </svg>

        <p class="lifetime">
          累计播放 {{ number(data.lifetime.plays) }} · 累计获赞 {{ number(data.lifetime.likes) }} ·
          粉丝 {{ number(data.lifetime.followers) }}
        </p>
      </section>

      <section class="panel">
        <header class="panel-head">
          <h2>作品表现</h2>
          <span class="panel-meta">按周期播放排序</span>
        </header>

        <ul v-if="data.top.length" class="works">
          <li v-for="(work, index) in data.top" :key="work.id">
            <span class="rank">{{ index + 1 }}</span>
            <NuxtLink class="work" :to="`/${work.type}/${work.slug}`">
              <span class="title">{{ work.title || '（无标题）' }}</span>
              <span class="kind">{{ work.type === 'image' ? '图集' : '视频' }}</span>
            </NuxtLink>
            <span class="metrics">
              <span><b>{{ number(work.plays) }}</b>播放</span>
              <span><b>{{ number(work.likes) }}</b>赞</span>
              <span><b>{{ number(work.comments) }}</b>评论</span>
            </span>
          </li>
        </ul>
        <p v-else class="empty">还没有作品，<NuxtLink to="/publish">去发布</NuxtLink>。</p>
      </section>
          </template>
        </VpAsyncState>
      </template>

      <StudioWorks v-else-if="tab === 'works'" />
      <StudioComments v-else />
    </template>
  </main>
</template>

<style scoped>
.studio {
  min-height: 100%;
  padding: 18px 18px calc(40px + env(safe-area-inset-bottom));
  box-sizing: border-box;
  color: var(--vp-tool-text);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: var(--vp-layout-content-max); margin: 0 auto 14px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid var(--vp-tool-border-strong);
  color: var(--vp-tool-text); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.range { margin-left: auto; display: flex; gap: 4px; padding: 3px; border-radius: 99px; background: var(--vp-tool-surface-2); }
.range button {
  padding: 5px 13px; border: 0; border-radius: 99px; cursor: pointer;
  background: transparent; color: var(--vp-tool-text-2); font-size: 12px;
}
.range button.on { background: var(--vp-tool-accent-soft); color: var(--vp-tool-accent-ink); font-weight: 600; }

.studio-tabs { max-width: var(--vp-layout-content-max); margin: 0 auto 14px; display: flex; gap: 6px; }
.studio-tabs button {
  padding: 6px 15px; border: 0; border-radius: 99px; cursor: pointer;
  background: var(--vp-tool-surface-2); color: var(--vp-tool-text-2);
  font-size: 12px;
}
.studio-tabs button.on { background: var(--vp-tool-accent-soft); color: var(--vp-tool-accent-ink); font-weight: 600; }

.cards {
  max-width: var(--vp-layout-content-max); margin: 0 auto 14px;
  display: grid; gap: 10px; grid-template-columns: repeat(4, minmax(0, 1fr));
}
.card {
  padding: 12px 14px; border-radius: 14px;
  background: var(--vp-tool-surface);
  border: 1px solid var(--vp-tool-border);
  display: grid; gap: 4px;
}
.card .label { font-size: 11px; color: var(--vp-tool-text-2); }
.card .value { font-size: 22px; font-weight: 600; line-height: 1.1; }
.card .change { font-size: 11px; color: var(--vp-tool-text-2); display: flex; align-items: baseline; gap: 4px; }
.card .change.up { color: var(--vp-tool-positive); }
.card .change.down { color: var(--vp-tool-danger-ink); }
.card .change small { font-size: 10px; opacity: 0.7; }

.panel {
  max-width: var(--vp-layout-content-max); margin: 0 auto 14px; padding: 14px 16px;
  border-radius: 16px; background: var(--vp-tool-surface);
  border: 1px solid var(--vp-tool-border);
  /* 没有全局 reset，默认 content-box 会让面板比上面的卡片行宽出内边距。 */
  box-sizing: border-box;
}
.panel-head { display: flex; align-items: baseline; gap: 10px; margin-bottom: 10px; }
.panel-head h2 { margin: 0; font-size: 13px; font-weight: 600; }
.panel-meta { margin-left: auto; font-size: 11px; color: var(--vp-tool-text-2); }

.chart { width: 100%; height: 170px; display: block; }
.chart .grid { stroke: var(--vp-tool-chart-grid); stroke-width: 1; }
.chart .bar { fill: var(--vp-tool-chart-bar); opacity: 0.55; }
.chart .bar.today { fill: var(--vp-tool-accent); opacity: 0.95; }
.chart .axis { fill: var(--vp-tool-text-2); font-size: 10px; }

.lifetime { margin: 8px 0 0; font-size: 11px; color: var(--vp-tool-text-2); }

.works { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
.works li { display: flex; align-items: center; gap: 10px; padding: 8px 0; border-top: 1px solid var(--vp-tool-divider); }
.works li:first-child { border-top: 0; }
.rank {
  width: 20px; height: 20px; flex: none; display: grid; place-items: center;
  border-radius: 6px; background: var(--vp-tool-surface-2);
  font-size: 11px; color: var(--vp-tool-text-2);
}
.work { flex: 1; min-width: 0; display: flex; align-items: center; gap: 8px; text-decoration: none; color: inherit; }
.work .title { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.work .kind { flex: none; font-size: 10px; color: var(--vp-tool-text-2); border: 1px solid var(--vp-tool-input-border); border-radius: 4px; padding: 1px 5px; }
.metrics { display: flex; gap: 12px; font-size: 11px; color: var(--vp-tool-text-2); }
.metrics b { color: var(--vp-tool-text); font-weight: 600; margin-right: 2px; }

.hint, .gate { max-width: var(--vp-layout-content-max); margin: 40px auto; text-align: center; font-size: 13px; color: var(--vp-tool-text-2); }
.gate .cta { display: inline-block; margin-top: 12px; padding: 9px 20px; border-radius: 99px; border: 0; cursor: pointer; font-family: inherit; background: var(--vp-tool-accent); color: var(--vp-tool-on-accent); text-decoration: none; font-size: 13px; }
.notice { max-width: var(--vp-layout-content-max); margin: 0 auto 12px; padding: 10px 12px; border-radius: 10px; background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink); font-size: 12px; box-sizing: border-box; }
.empty { margin: 18px 0; font-size: 12px; color: var(--vp-tool-text-2); text-align: center; }
.empty a { color: var(--vp-tool-accent-ink); }

@media (max-width: 640px) {
  .cards { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .metrics span:nth-child(2) { display: none; }
}
</style>
