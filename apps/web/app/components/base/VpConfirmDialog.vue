<script setup lang="ts">
// 删除 / 移除类操作的统一二次确认弹窗：正文一句话说明后果，
// 确认按钮用危险色。确认后保持 open + busy 直到调用方收尾，
// 失败信息由调用方页面自己展示（与「删除草稿」弹窗同一约定）。
const props = withDefaults(defineProps<{
  open: boolean
  title: string
  busy?: boolean
  confirmText?: string
  busyText?: string
}>(), { busy: false, confirmText: '确认删除', busyText: '删除中…' })

const emit = defineEmits<{ confirm: []; close: [] }>()
</script>

<template>
  <VpDialog :open="props.open" :title="props.title" :busy="props.busy" @close="emit('close')">
    <p class="dialog-text"><slot /></p>
    <div class="dialog-actions">
      <button type="button" class="btn" @click="emit('close')">取消</button>
      <button type="button" class="btn danger" :disabled="props.busy" @click="emit('confirm')">
        {{ props.busy ? props.busyText : props.confirmText }}
      </button>
    </div>
  </VpDialog>
</template>

<style scoped>
.dialog-text {
  margin: 0 0 14px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--vp-tool-text-2);
}
.dialog-actions { display: flex; justify-content: flex-end; gap: 10px; }
.btn {
  padding: 9px 18px;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  border: 1px solid var(--vp-tool-border);
  background: transparent;
  color: inherit;
}
.btn.danger {
  border-color: var(--vp-tool-danger-border);
  color: var(--vp-tool-danger-ink);
}
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
</style>
