<script setup lang="ts">
// 下拉选择（内容页 Vp* 组件）：原生 <select> 的弹层由系统绘制，深色界面里会
// 冒出白底蓝选项，因此内容页一律用这个自绘下拉，配色全部走设计令牌。
interface SelectOption {
  value: string | number
  label: string
}

const props = defineProps<{
  modelValue: string | number
  options: SelectOption[]
  label?: string
  ariaLabel?: string
  disabled?: boolean
  /** pill：筛选条上的小胶囊；field：表单里与外层输入框同尺寸 */
  variant?: 'pill' | 'field'
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string | number]
  change: [value: string | number]
}>()

let seq = 0

const root = ref<HTMLElement | null>(null)
const panelId = `vp-select-panel-${++seq}`
const open = ref(false)
const activeIndex = ref(0)

const currentLabel = computed(() => {
  const hit = props.options.find((item) => item.value === props.modelValue)
  return hit ? hit.label : props.options[0]?.label ?? ''
})

function close() {
  open.value = false
}

function openPanel() {
  if (props.disabled) return
  open.value = true
  const index = props.options.findIndex((item) => item.value === props.modelValue)
  activeIndex.value = index >= 0 ? index : 0
}

function toggle() {
  if (open.value) close()
  else openPanel()
}

function select(option: SelectOption) {
  close()
  if (option.value === props.modelValue) return
  emit('update:modelValue', option.value)
  emit('change', option.value)
}

function onKeydown(event: KeyboardEvent) {
  if (props.disabled) return
  switch (event.key) {
    case 'Enter':
    case ' ':
      event.preventDefault()
      if (!open.value) { openPanel(); return }
      if (props.options[activeIndex.value]) select(props.options[activeIndex.value] as SelectOption)
      break
    case 'ArrowDown':
      event.preventDefault()
      if (!open.value) { openPanel(); return }
      activeIndex.value = Math.min(props.options.length - 1, activeIndex.value + 1)
      break
    case 'ArrowUp':
      event.preventDefault()
      if (!open.value) { openPanel(); return }
      activeIndex.value = Math.max(0, activeIndex.value - 1)
      break
    case 'Home':
      if (open.value) { event.preventDefault(); activeIndex.value = 0 }
      break
    case 'End':
      if (open.value) { event.preventDefault(); activeIndex.value = props.options.length - 1 }
      break
    case 'Escape':
      if (open.value) { event.preventDefault(); close() }
      break
    case 'Tab':
      close()
      break
  }
}

function onPointerDown(event: PointerEvent) {
  if (!root.value?.contains(event.target as Node)) close()
}

onMounted(() => document.addEventListener('pointerdown', onPointerDown))
onBeforeUnmount(() => document.removeEventListener('pointerdown', onPointerDown))
</script>

<template>
  <div
    ref="root" class="vp-select"
    :class="{ 'is-open': open, 'is-disabled': props.disabled, 'is-field': props.variant === 'field' }"
  >
    <span v-if="props.label" class="vp-select__label">{{ props.label }}</span>
    <div class="vp-select__control">
      <button
        type="button" class="vp-select__trigger"
        role="combobox" :aria-expanded="open" aria-haspopup="listbox" :aria-controls="panelId"
        :aria-label="props.ariaLabel || props.label"
        :disabled="props.disabled"
        @click="toggle" @keydown="onKeydown"
      >
        <span class="vp-select__value">{{ currentLabel }}</span>
        <ImIcon class="vp-select__caret" name="chevron" :size="14" />
      </button>

      <ul v-if="open" :id="panelId" class="vp-select__panel" role="listbox">
        <li
          v-for="(option, index) in props.options" :key="option.value"
          role="option" :aria-selected="option.value === props.modelValue"
          :class="{
            'is-selected': option.value === props.modelValue,
            'is-active': index === activeIndex,
          }"
          @mouseenter="activeIndex = index"
          @click="select(option)"
        >
          {{ option.label }}
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.vp-select { position: relative; display: inline-flex; align-items: center; gap: 6px; }
.vp-select__label { font-size: 12px; color: var(--vp-tool-text-2); }
/* 面板相对触发按钮定位，而不是整组（否则带前置文案时会向左偏）。 */
.vp-select__control { position: relative; display: block; }

.vp-select__trigger {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 30px;
  padding: 0 10px 0 12px;
  border: 1px solid var(--vp-tool-input-border);
  border-radius: 99px;
  background: var(--vp-tool-surface-2);
  color: var(--vp-tool-text);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  transition: border-color var(--vp-motion-instant) var(--vp-ease-out),
    background var(--vp-motion-instant) var(--vp-ease-out);
}
.vp-select__trigger:hover { border-color: var(--vp-tool-border-strong); background: var(--vp-tool-hover); }
.vp-select.is-open .vp-select__trigger { border-color: var(--vp-tool-accent-ink); }
.vp-select.is-disabled .vp-select__trigger { opacity: 0.5; cursor: not-allowed; }
.vp-select__value { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.vp-select__caret {
  color: var(--vp-tool-text-2);
  transition: transform var(--vp-motion-standard) var(--vp-ease-out);
}
.vp-select.is-open .vp-select__caret { transform: rotate(180deg); color: var(--vp-tool-accent-ink); }

/* 面板：深色表面 + 品牌色选中态，层级压在内容之上、抽屉之下。 */
.vp-select.is-field { display: block; width: 100%; }
.vp-select.is-field .vp-select__trigger {
  width: 100%;
  height: 40px;
  padding: 0 11px;
  border-radius: 9px;
  justify-content: space-between;
  background: var(--vp-tool-input);
  font-size: 13px;
}
.vp-select.is-field .vp-select__panel { width: 100%; }

.vp-select__panel {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 60;
  min-width: 100%;
  max-height: 260px;
  margin: 0;
  padding: 6px;
  list-style: none;
  overflow-y: auto;
  border-radius: 12px;
  border: 1px solid var(--vp-tool-border-strong);
  background: var(--vp-tool-panel);
  box-shadow: var(--vp-tool-panel-shadow);
}
.vp-select__panel li {
  padding: 8px 10px;
  border-radius: 8px;
  font-size: 12px;
  color: var(--vp-tool-text);
  white-space: nowrap;
  cursor: pointer;
}
.vp-select__panel li.is-active { background: var(--vp-tool-item-active); }
.vp-select__panel li.is-selected { color: var(--vp-tool-accent-ink); font-weight: 600; }
.vp-select__panel li.is-selected.is-active { background: var(--vp-tool-accent-soft); }
</style>
