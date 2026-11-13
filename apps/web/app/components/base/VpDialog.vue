<script setup lang="ts">
// 统一对话框 / 抽屉壳（UI 规范 §11.3 / §15 / §17.2）：
// 焦点进入、Tab 焦点陷阱、Escape 关闭、外点关闭、关闭后焦点返回触发控件。
// 深浅主题都消费 --vp-tool-* 令牌：工具页跟随主题，
// 沉浸页（如「我」页申诉）拿到 :root 的深色默认值，恒深色。
import { nextTick, ref, useId, watch } from 'vue'

const props = withDefaults(defineProps<{
  open: boolean
  title: string
  variant?: 'center' | 'sheet'
  /** 提交中：阻止 Escape / 外点关闭，避免写到一半被关掉 */
  busy?: boolean
  /** 打开时的优先焦点选择器（如 '.field input'）；缺省聚焦第一个可聚焦控件 */
  initialFocus?: string
}>(), { variant: 'center', busy: false, initialFocus: '' })

const emit = defineEmits<{ close: [] }>()

const dialog = ref<HTMLElement | null>(null)
const titleId = `vp-dialog-${useId()}`
let trigger: HTMLElement | null = null

const FOCUSABLE = [
  'a[href]', 'button:not([disabled])', 'input:not([disabled])',
  'textarea:not([disabled])', 'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusables(): HTMLElement[] {
  if (!dialog.value) return []
  return Array.from(dialog.value.querySelectorAll<HTMLElement>(FOCUSABLE))
    .filter((el) => el.offsetParent !== null || el === document.activeElement)
}

function requestClose() {
  if (props.busy) return
  emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    requestClose()
    return
  }
  if (event.key !== 'Tab') return
  // 焦点陷阱：把 Tab / Shift+Tab 限制在对话框内循环。
  const list = focusables()
  if (!list.length) {
    event.preventDefault()
    dialog.value?.focus()
    return
  }
  const first = list[0]!
  const last = list[list.length - 1]!
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === dialog.value)) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(() => props.open, async (open) => {
  if (!import.meta.client) return
  if (open) {
    trigger = document.activeElement as HTMLElement | null
    await nextTick()
    // 优先调用方指定的控件（登录框直接进输入框），否则第一个可聚焦控件，
    // 都没有则对话框本身（tabindex=-1 可脚本聚焦）。
    const preferred = props.initialFocus
      ? dialog.value?.querySelector<HTMLElement>(props.initialFocus) ?? null
      : null
    ;(preferred ?? focusables()[0] ?? dialog.value)?.focus()
  } else {
    await nextTick()
    // 关闭后焦点必须回到触发控件；控件已卸载时回落到 body 不报错。
    if (trigger && trigger.isConnected) trigger.focus()
    trigger = null
  }
})
</script>

<template>
  <div
    v-if="props.open" class="vp-dialog-layer" :class="`is-${props.variant}`"
    @click.self="requestClose" @keydown="onKeydown"
  >
    <div
      ref="dialog" class="vp-dialog" role="dialog" aria-modal="true"
      :aria-labelledby="titleId" tabindex="-1"
    >
      <h2 :id="titleId" class="vp-dialog-title">{{ props.title }}</h2>
      <slot />
    </div>
  </div>
</template>

<style scoped>
.vp-dialog-layer {
  position: fixed; inset: 0; z-index: 60; padding: 20px;
  background: var(--vp-tool-overlay);
  display: grid; place-items: center;
}
.vp-dialog-layer.is-sheet { place-items: end center; padding: 0; }
.vp-dialog {
  width: min(380px, 100%); padding: 18px;
  border-radius: 16px;
  background: var(--vp-tool-dialog);
  border: 1px solid var(--vp-tool-dialog-border);
  color: var(--vp-tool-text);
  box-shadow: var(--vp-tool-panel-shadow);
  max-height: min(80vh, 640px); overflow-y: auto;
}
.is-sheet .vp-dialog {
  width: min(520px, 100%);
  border-radius: 20px 20px 0 0; border-bottom: 0;
  padding-bottom: calc(18px + env(safe-area-inset-bottom));
}
.vp-dialog-title { margin: 0 0 12px; font-size: 15px; font-weight: 600; }
.vp-dialog :where(button, a, input, textarea):focus-visible {
  box-shadow: 0 0 0 3px var(--vp-tool-focus);
}
</style>
