<script setup lang="ts">
import type { AdminTrendPoint } from '~/composables/useAdminApi'

const props = withDefaults(defineProps<{
  points: AdminTrendPoint[]
  loading?: boolean
}>(), { loading: false })

const WIDTH = 640
const HEIGHT = 220
const PAD_LEFT = 44
const PAD_RIGHT = 12
const PAD_TOP = 16
const PAD_BOTTOM = 34
const CHART_W = WIDTH - PAD_LEFT - PAD_RIGHT
const CHART_H = HEIGHT - PAD_TOP - PAD_BOTTOM

const maxValue = computed(() => {
  const values = props.points.flatMap((point) => [point.plays, point.new_users])
  return Math.max(1, ...values)
})

const ticks = computed(() => {
  const step = maxValue.value / 4
  return [0, 1, 2, 3, 4].map((index) => {
    const value = Math.round(step * index)
    return {
      value,
      y: PAD_TOP + CHART_H - (value / maxValue.value) * CHART_H,
    }
  })
})

const groups = computed(() => {
  const count = Math.max(props.points.length, 1)
  const slot = CHART_W / count
  return props.points.map((point, index) => {
    const center = PAD_LEFT + slot * index + slot / 2
    const barWidth = Math.min(16, slot / 3.4)
    const playHeight = (point.plays / maxValue.value) * CHART_H
    const userHeight = (point.new_users / maxValue.value) * CHART_H
    return {
      ...point,
      label: point.day.slice(5),
      playX: center - barWidth - 2,
      playY: PAD_TOP + CHART_H - playHeight,
      playHeight: Math.max(playHeight, point.plays > 0 ? 2 : 0),
      userX: center + 2,
      userY: PAD_TOP + CHART_H - userHeight,
      userHeight: Math.max(userHeight, point.new_users > 0 ? 2 : 0),
      barWidth,
      center,
    }
  })
})
</script>

<template>
  <figure class="trend">
    <figcaption>
      <span class="title">近 7 日播放与新增用户</span>
      <span class="legend">
        <i class="dot plays" />播放量
        <i class="dot users" />新增用户
      </span>
    </figcaption>

    <div v-if="loading" class="placeholder">加载趋势数据…</div>
    <div v-else-if="!points.length" class="placeholder">暂无趋势数据</div>
    <svg v-else :viewBox="`0 0 ${WIDTH} ${HEIGHT}`" role="img"
         aria-label="近 7 日播放量与新增用户双指标柱状图">
      <g class="grid">
        <template v-for="tick in ticks" :key="tick.value">
          <line :x1="PAD_LEFT" :x2="WIDTH - PAD_RIGHT" :y1="tick.y" :y2="tick.y" />
          <text :x="PAD_LEFT - 8" :y="tick.y + 4" text-anchor="end">{{ tick.value }}</text>
        </template>
      </g>
      <g v-for="group in groups" :key="group.day">
        <rect
          class="bar plays" :x="group.playX" :y="group.playY"
          :width="group.barWidth" :height="group.playHeight" rx="3"
        >
          <title>{{ group.day }} 播放量 {{ group.plays }}</title>
        </rect>
        <rect
          class="bar users" :x="group.userX" :y="group.userY"
          :width="group.barWidth" :height="group.userHeight" rx="3"
        >
          <title>{{ group.day }} 新增用户 {{ group.new_users }}</title>
        </rect>
        <text class="day" :x="group.center" :y="HEIGHT - 12" text-anchor="middle">
          {{ group.label }}
        </text>
      </g>
    </svg>
  </figure>
</template>

<style scoped>
.trend {
  margin: 0;
  padding: 16px 18px 10px;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.07);
}
figcaption {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: 8px;
}
.title { font-size: 14px; font-weight: 600; color: #f4f4f5; }
.legend { display: flex; align-items: center; gap: 6px; font-size: 12px; color: #a1a1aa; }
.dot { width: 8px; height: 8px; border-radius: 2px; display: inline-block; }
.dot.plays { background: #ff2e7e; }
.dot.users { background: #22d3ee; margin-left: 10px; }
svg { width: 100%; height: auto; display: block; }
.grid line { stroke: rgba(255, 255, 255, 0.08); stroke-width: 1; }
.grid text { fill: #71717a; font-size: 10px; }
.bar.plays { fill: #ff2e7e; }
.bar.users { fill: #22d3ee; }
.day { fill: #8b8b93; font-size: 10px; }
.placeholder {
  height: 180px; display: grid; place-items: center;
  font-size: 13px; color: #8b8b93;
}
</style>
