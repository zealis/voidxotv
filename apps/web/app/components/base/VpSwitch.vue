<script setup lang="ts">
// 开关（内容页 Vp* 组件）：原生 checkbox 无法表达 §11.3 要求的状态层次，
// 这里用 role="switch" 的按钮实现，键盘 Space / Enter 均可切换。
const props = defineProps<{
  modelValue: boolean
  disabled?: boolean
  ariaLabel?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

function toggle() {
  if (props.disabled) return
  emit('update:modelValue', !props.modelValue)
}
</script>

<template>
  <button
    type="button" role="switch" class="vp-switch"
    :class="{ 'is-on': props.modelValue }"
    :aria-checked="props.modelValue"
    :aria-label="props.ariaLabel"
    :disabled="props.disabled"
    @click="toggle"
  >
    <span class="vp-switch__knob" aria-hidden="true" />
  </button>
</template>

<style scoped>
.vp-switch {
  position: relative;
  flex: none;
  width: 44px;
  height: 26px;
  padding: 0;
  border: 0;
  border-radius: 99px;
  background: var(--vp-tool-track);
  cursor: pointer;
  transition: background var(--vp-motion-exit) var(--vp-ease-out);
}
.vp-switch.is-on { background: var(--vp-tool-accent); }
.vp-switch:disabled { opacity: 0.5; cursor: not-allowed; }
.vp-switch__knob {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--vp-tool-knob);
  box-shadow: var(--vp-tool-knob-shadow);
  transition: transform var(--vp-motion-exit) var(--vp-ease-out);
}
.vp-switch.is-on .vp-switch__knob { transform: translateX(18px); }
.vp-switch:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--vp-tool-focus);
}
</style>
