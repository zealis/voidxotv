<script setup lang="ts">
// 后台状态徽标（UI 规范 §4.10：「状态徽标：已上架为青色、审核中为粉色」）。
// 全站只用设计文档里的四个语义色（青 / 品牌粉 / Danger / 中性）＋统一描边，
// 不混用 Element Plus 默认的成功绿、警告黄，避免与品牌色串味。
type Tone = 'positive' | 'attention' | 'danger' | 'neutral'

withDefaults(defineProps<{ text: string; tone?: Tone }>(), { tone: 'neutral' })
</script>

<template>
  <span class="status-tag" :class="`is-${tone}`">{{ text }}</span>
</template>

<style scoped>
.status-tag {
  --status-color: #a1a1aa;
  --status-fill: rgba(255, 255, 255, 0.04);
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 8px;
  border: 1px solid currentColor;
  border-radius: 4px;
  background: var(--status-fill);
  color: var(--status-color);
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  letter-spacing: 0.02em;
  white-space: nowrap;
  vertical-align: middle;
}
/* 青：正向、已上架、可用、已成立 */
.status-tag.is-positive {
  --status-color: #26deed;
  --status-fill: rgba(38, 222, 237, 0.1);
}
/* 粉：品牌主色，用于待处理 / 审核中等需要注意的状态 */
.status-tag.is-attention {
  --status-color: #ff2e7e;
  --status-fill: rgba(255, 46, 126, 0.1);
}
/* Danger：已驳回、封禁、异常等负向结果 */
.status-tag.is-danger {
  --status-color: #ff6961;
  --status-fill: rgba(255, 105, 97, 0.1);
}
/* 中性：已下线、已停用、不成立等无情绪状态 */
.status-tag.is-neutral {
  --status-color: #a1a1aa;
  --status-fill: rgba(255, 255, 255, 0.04);
}
</style>
