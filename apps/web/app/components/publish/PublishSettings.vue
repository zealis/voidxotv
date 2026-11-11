<script setup lang="ts">
// 发布设置三项（UI 规范 §8.1）：谁可以看 / 添加位置 / 发布时间。
// 逐行呈现、右侧显示当前值；定时用自绘「日期 + 时段」控件替代原生
// datetime-local（原生控件在深色界面里是系统白底，且无法统一键盘行为）。
export interface POIValue { name: string; address: string }

const props = defineProps<{
  visibility: string
  poi: POIValue | null
  scheduledAt: string   // ISO 字符串，空串表示立即发布
  disabled?: boolean
}>()
const emit = defineEmits<{
  'update:visibility': [string]
  'update:poi': [POIValue | null]
  'update:scheduledAt': [string]
}>()

const visibilityOptions = [
  { value: 'public', label: '所有人可见' },
  { value: 'friends', label: '好友可见' },
  { value: 'private', label: '仅自己' },
]

// ── 位置 ──────────────────────────────────────────────────────
const poiOpen = ref(false)
const poiName = ref('')
const poiAddress = ref('')
watch(() => props.poi, (value) => {
  poiName.value = value?.name || ''
  poiAddress.value = value?.address || ''
})

function savePoi() {
  const name = poiName.value.trim()
  if (!name) {
    emit('update:poi', null)
  } else {
    emit('update:poi', { name, address: poiAddress.value.trim() })
  }
  poiOpen.value = false
}
function clearPoi() {
  poiName.value = ''
  poiAddress.value = ''
  poiOpen.value = false
  emit('update:poi', null)
}

// ── 发布时间 ──────────────────────────────────────────────────
// 服务端与前端同一口径：定时不得早于当前时间 10 分钟（§6.1 / §8.1）。
const MIN_LEAD_MS = 10 * 60 * 1000

interface DayOption { value: string; label: string }
const dayOptions = computed<DayOption[]>(() => {
  const out: DayOption[] = []
  const now = new Date()
  for (let i = 0; i < 30; i++) {
    const day = new Date(now.getFullYear(), now.getMonth(), now.getDate() + i)
    const value = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, '0')}-${String(day.getDate()).padStart(2, '0')}`
    const label = i === 0 ? '今天' : i === 1 ? '明天'
      : `${day.getMonth() + 1} 月 ${day.getDate()} 日`
    out.push({ value, label })
  }
  return out
})
const hourOptions = Array.from({ length: 24 }, (_, i) => ({ value: String(i).padStart(2, '0'), label: `${String(i).padStart(2, '0')} 时` }))
const minuteOptions = Array.from({ length: 12 }, (_, i) => ({ value: String(i * 5).padStart(2, '0'), label: `:${String(i * 5).padStart(2, '0')}` }))

const scheduled = ref(false)
const day = ref(dayOptions.value[0]?.value || '')
const hour = ref('20')
const minute = ref('00')
// 外部值只在首次（草稿回填）同步一次；之后控件单向 emit，避免
// 「5 分钟粒度控件」把回填的 :07 改写成 :05，也避免 watch 互相触发。
let hydrated = false
let restoring = false

function toISO(value: Date): string {
  return value.toISOString()
}

// 不用 immediate：组件挂载时 props 还是空串，若此时锁定「已同步」，
// 后续草稿回填的真实定时值会被忽略。空值不锁定，等首次非空值。
watch(() => props.scheduledAt, (value) => {
  if (hydrated || restoring || !value) return
  hydrated = true
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return
  scheduled.value = true
  day.value = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
  hour.value = String(date.getHours()).padStart(2, '0')
  minute.value = String(Math.floor(date.getMinutes() / 5) * 5).padStart(2, '0')
}, { immediate: true })

const composeDate = computed(() => {
  const [y, m, d] = day.value.split('-').map(Number)
  if (!y || !m || !d) return null
  const value = new Date(y, m - 1, d, Number(hour.value), Number(minute.value), 0, 0)
  return Number.isNaN(value.getTime()) ? null : value
})
const leadMs = computed(() => composeDate.value ? composeDate.value.getTime() - Date.now() : 0)
const tooSoon = computed(() => scheduled.value && leadMs.value < MIN_LEAD_MS)
const scheduleError = computed(() => {
  if (!scheduled.value) return ''
  if (!composeDate.value) return '请选择有效的日期与时间'
  if (tooSoon.value) return '定时时间须至少晚于当前时间 10 分钟'
  return ''
})
const scheduleText = computed(() => {
  if (!scheduled.value) return '立即发布'
  if (!composeDate.value) return '待选择'
  return composeDate.value.toLocaleString('zh-CN', {
    hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  })
})

function emitSchedule() {
  if (restoring) return
  const next = !scheduled.value || !composeDate.value || tooSoon.value
    ? ''
    : toISO(composeDate.value)
  if (next === props.scheduledAt) return
  restoring = true
  emit('update:scheduledAt', next)
  queueMicrotask(() => { restoring = false })
}

watch([scheduled, day, hour, minute], emitSchedule)

defineExpose({ scheduleError })
</script>

<template>
  <div class="settings-rows">
    <!-- 1 · 谁可以看 -->
    <div class="row">
      <div class="label"><strong>谁可以看</strong><small>默认沿用设置页的隐私偏好</small></div>
      <div class="value">
        <VpSelect
          :model-value="props.visibility" :options="visibilityOptions"
          :disabled="props.disabled" aria-label="谁可以看"
          @change="emit('update:visibility', String($event))"
        />
      </div>
    </div>

    <!-- 2 · 添加位置 -->
    <div class="row">
      <div class="label"><strong>添加位置</strong><small>可选，展示在作品信息中</small></div>
      <div class="value poi-value">
        <span v-if="props.poi" class="poi-name">{{ props.poi.name }}</span>
        <span v-else class="muted">未添加</span>
        <button type="button" class="link" :disabled="props.disabled" @click="poiOpen = !poiOpen">
          {{ props.poi ? '修改' : '添加' }}
        </button>
        <button v-if="props.poi" type="button" class="link danger" :disabled="props.disabled" @click="clearPoi">清除</button>
      </div>
    </div>
    <div v-if="poiOpen" class="poi-editor">
      <label class="mini-field">
        <span>位置名称</span>
        <input v-model="poiName" type="text" maxlength="60" :disabled="props.disabled" aria-label="位置名称" />
      </label>
      <label class="mini-field">
        <span>详细地址（可选）</span>
        <input v-model="poiAddress" type="text" maxlength="120" :disabled="props.disabled" aria-label="详细地址" />
      </label>
      <div class="editor-actions">
        <button type="button" class="link" :disabled="props.disabled" @click="poiOpen = false">取消</button>
        <button type="button" class="solid" :disabled="props.disabled" @click="savePoi">保存位置</button>
      </div>
    </div>

    <!-- 3 · 发布时间 -->
    <div class="row">
      <div class="label"><strong>发布时间</strong><small>定时须至少晚于当前 10 分钟</small></div>
      <div class="value schedule-value">
        <div class="segment" role="group" aria-label="发布时机">
          <button
            type="button" :class="{ on: !scheduled }" :aria-pressed="!scheduled"
            :disabled="props.disabled" @click="scheduled = false"
          >立即</button>
          <button
            type="button" :class="{ on: scheduled }" :aria-pressed="scheduled"
            :disabled="props.disabled" @click="scheduled = true"
          >定时</button>
        </div>
      </div>
    </div>
    <div v-if="scheduled" class="schedule-editor">
      <div class="time-selects">
        <VpSelect :model-value="day" :options="dayOptions" :disabled="props.disabled" aria-label="定时日期" @change="day = String($event)" />
        <VpSelect :model-value="hour" :options="hourOptions" :disabled="props.disabled" aria-label="定时小时" @change="hour = String($event)" />
        <VpSelect :model-value="minute" :options="minuteOptions" :disabled="props.disabled" aria-label="定时分钟" @change="minute = String($event)" />
      </div>
      <p class="schedule-summary" :class="{ err: !!scheduleError }" role="status">
        {{ scheduleError || `将于 ${scheduleText} 发布` }}
      </p>
    </div>
  </div>
</template>

<style scoped>
.settings-rows { display: grid; }
.row {
  display: flex; align-items: center; gap: 12px; padding: 10px 0;
  border-top: 1px solid var(--vp-tool-divider);
}
.row:first-child { border-top: 0; }
.label { flex: 1; min-width: 0; display: grid; gap: 3px; }
.label strong { font-size: 13px; font-weight: 500; color: var(--vp-tool-text); }
.label small { font-size: 11px; color: var(--vp-tool-text-2); }
.value { flex: none; display: flex; align-items: center; gap: 8px; }
.muted { font-size: 12px; color: var(--vp-tool-text-2); }
.poi-name {
  font-size: 12px; color: var(--vp-tool-text); max-width: 160px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.link {
  border: 0; background: none; padding: 3px 2px; cursor: pointer;
  color: var(--vp-tool-accent-ink); font-size: 12px; font-family: inherit;
}
.link.danger { color: var(--vp-tool-danger-ink); }
.link:disabled { opacity: .55; cursor: not-allowed; }
.solid {
  padding: 6px 14px; border-radius: 8px; cursor: pointer; font-size: 12px; font-family: inherit;
  border: 1px solid var(--vp-tool-border-strong);
  background: var(--vp-tool-surface-2); color: var(--vp-tool-text);
}
.poi-editor, .schedule-editor {
  padding: 10px 12px; margin: 0 0 8px; border-radius: 10px;
  border: 1px solid var(--vp-tool-border); background: var(--vp-tool-surface);
}
.mini-field { display: grid; gap: 5px; margin-bottom: 8px; }
.mini-field span { font-size: 11px; color: var(--vp-tool-text-2); }
.mini-field input {
  width: 100%; box-sizing: border-box; padding: 7px 9px; border-radius: 8px;
  border: 1px solid var(--vp-tool-input-border); font-size: 12px; font-family: inherit;
  background: var(--vp-tool-input); color: var(--vp-tool-text);
}
.editor-actions { display: flex; justify-content: flex-end; gap: 12px; align-items: center; }
.time-selects { display: flex; gap: 8px; flex-wrap: wrap; }
.segment { display: inline-flex; gap: 3px; padding: 3px; border-radius: 99px; background: var(--vp-tool-surface-2); }
.segment button {
  padding: 5px 14px; border: 0; border-radius: 99px; cursor: pointer;
  background: transparent; color: var(--vp-tool-text-2); font-size: 12px; font-family: inherit;
}
.segment button.on { background: var(--vp-tool-accent-soft); color: var(--vp-tool-accent-ink); font-weight: 600; }
.segment button:disabled { opacity: .55; cursor: not-allowed; }
.schedule-summary { margin: 9px 0 0; font-size: 11px; color: var(--vp-tool-text-2); }
.schedule-summary.err { color: var(--vp-tool-danger-ink); }
@media (max-width: 560px) {
  .row { flex-wrap: wrap; }
  .value { flex: 1 1 100%; }
}
</style>
