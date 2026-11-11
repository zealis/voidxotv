<script setup lang="ts">
// 数据驱动界面的统一状态矩阵（UI 规范 §12）：
// Loading/Skeleton（与内容同形）、Empty（原因 + 下一步）、Error（原因 + Retry）、
// Partial（部分数据）、离线与权限不足。组件只消费语义令牌，工具页跟随深浅主题，
// 沉浸页拿到 :root 深色默认值。
withDefaults(defineProps<{
  /** ready 时才渲染默认插槽 */
  state: 'ready' | 'loading' | 'empty' | 'error' | 'offline' | 'forbidden'
  error?: string
  emptyTitle?: string
  emptyHint?: string
  /** 骨架与真实内容同形：list=行列表，cards=四张指标卡，panel=单个面板 */
  variant?: 'list' | 'cards' | 'panel'
  rows?: number
  /** 已有数据但本次刷新失败：保留旧内容并标注「部分数据」 */
  partial?: boolean
  retrying?: boolean
}>(), {
  error: '', emptyTitle: '这里还没有内容', emptyHint: '',
  variant: 'list', rows: 3, partial: false, retrying: false,
})

const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="vp-async">
    <!-- 部分数据：不隐藏已有内容，显式标注不完整（§12 Partial Data） -->
    <p v-if="partial && state === 'ready'" class="vp-partial" role="status">
      部分数据可能不是最新，<button type="button" class="link" @click="emit('retry')">重新加载</button>
    </p>

    <!-- 骨架：与内容同形；减少动效时静止、不扫光（§13.3） -->
    <div v-if="state === 'loading'" class="vp-skeleton" role="status" aria-label="正在加载">
      <template v-if="variant === 'cards'">
        <div class="sk-cards">
          <div v-for="i in 4" :key="i" class="sk-card"><span class="sk-line w40" /><span class="sk-line w70 tall" /></div>
        </div>
        <div class="sk-panel tall" />
      </template>
      <template v-else-if="variant === 'panel'">
        <div class="sk-panel tall" />
      </template>
      <template v-else>
        <div v-for="i in rows" :key="i" class="sk-row">
          <span class="sk-avatar" />
          <span class="sk-lines"><span class="sk-line w55" /><span class="sk-line w35" /></span>
        </div>
      </template>
      <span class="sr-only">正在加载…</span>
    </div>

    <!-- 空态：说明原因 + 明确下一步动作 -->
    <div v-else-if="state === 'empty'" class="vp-state" role="status">
      <span class="vp-state-glyph" aria-hidden="true">○</span>
      <p class="vp-state-title">{{ emptyTitle }}</p>
      <p v-if="emptyHint" class="vp-state-hint">{{ emptyHint }}</p>
      <div class="vp-state-action"><slot name="empty" /></div>
    </div>

    <!-- 错误 / 离线 / 权限不足：说明问题 + 恢复动作，不只靠颜色 -->
    <div v-else-if="state !== 'ready'" class="vp-state" role="alert">
      <span class="vp-state-glyph danger" aria-hidden="true">!</span>
      <p class="vp-state-title">
        {{ state === 'offline' ? '网络已断开' : state === 'forbidden' ? '登录状态已失效' : '加载失败' }}
      </p>
      <p class="vp-state-hint">
        {{ state === 'offline' ? '请检查网络连接后重试。'
          : state === 'forbidden' ? '请重新登录后再查看。'
            : (error || '暂时无法获取数据，请稍后重试。') }}
      </p>
      <div class="vp-state-action">
        <button
          v-if="state !== 'forbidden'" type="button" class="vp-retry"
          :disabled="retrying" @click="emit('retry')"
        >{{ retrying ? '重试中…' : '重试' }}</button>
        <slot name="action" />
      </div>
    </div>

    <slot v-else />
  </div>
</template>

<style scoped>
.vp-async { width: 100%; }
.sr-only {
  position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
  overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0;
}
.vp-partial {
  margin: 0 0 10px; padding: 8px 12px; border-radius: 10px; font-size: 12px;
  background: var(--vp-tool-danger-soft); color: var(--vp-tool-danger-ink);
}
.link {
  border: 0; background: none; padding: 0; cursor: pointer;
  color: inherit; text-decoration: underline; font: inherit;
}

/* 骨架：静态明暗区分，默认只做极轻微呼吸；减少动效时完全静止（§12.2 / §13.3） */
.vp-skeleton { display: grid; gap: 8px; animation: vp-breath 2.4s ease-in-out infinite; }
@keyframes vp-breath { 0%, 100% { opacity: 1 } 50% { opacity: 0.72 } }
html.vp-reduce-motion .vp-skeleton { animation: none; }
@media (prefers-reduced-motion: reduce) { .vp-skeleton { animation: none; } }

.sk-cards { display: grid; gap: 10px; grid-template-columns: repeat(4, minmax(0, 1fr)); }
.sk-card, .sk-panel, .sk-row {
  border-radius: 14px; border: 1px solid var(--vp-tool-border);
  background: var(--vp-tool-surface);
}
.sk-card { padding: 12px 14px; display: grid; gap: 8px; }
.sk-panel { padding: 14px 16px; }
.sk-panel.tall { min-height: 170px; }
.sk-row { display: flex; align-items: center; gap: 12px; padding: 10px 14px; }
.sk-avatar {
  flex: none; width: 34px; height: 34px; border-radius: 50%;
  background: var(--vp-tool-surface-2);
}
.sk-lines { display: grid; gap: 7px; flex: 1; }
.sk-line { display: block; height: 9px; border-radius: 5px; background: var(--vp-tool-surface-2); }
.sk-line.tall { height: 22px; }
.w35 { width: 35%; } .w40 { width: 40%; } .w55 { width: 55%; } .w70 { width: 70%; }

.vp-state {
  display: grid; justify-items: center; gap: 6px;
  padding: 26px 16px; text-align: center;
  border-radius: 14px; border: 1px solid var(--vp-tool-border);
  background: var(--vp-tool-surface);
}
.vp-state-glyph {
  display: grid; place-items: center; width: 30px; height: 30px;
  border-radius: 50%; font-size: 17px; font-weight: 700;
  border: 1px solid var(--vp-tool-border-strong); color: var(--vp-tool-text-2);
}
.vp-state-glyph.danger { border-color: var(--vp-tool-danger-border); color: var(--vp-tool-danger-ink); }
.vp-state-title { margin: 4px 0 0; font-size: 13px; font-weight: 600; color: var(--vp-tool-text); }
.vp-state-hint { margin: 0; font-size: 12px; line-height: 1.6; color: var(--vp-tool-text-2); }
.vp-state-action { margin-top: 8px; display: flex; gap: 10px; align-items: center; justify-content: center; }
/* 空态里的「去发布 / 去关注」等动作由页面插槽提供，统一成强调色可点链接。 */
.vp-state-action :slotted(a) {
  color: var(--vp-tool-accent-ink); font-size: 12px; text-decoration: none;
  border-bottom: 1px solid currentColor; padding-bottom: 1px;
}
.vp-retry {
  padding: 7px 18px; border-radius: 99px; cursor: pointer;
  border: 1px solid var(--vp-tool-border-strong);
  background: transparent; color: var(--vp-tool-text); font-size: 12px; font-family: inherit;
}
.vp-retry:disabled { opacity: 0.6; cursor: not-allowed; }

@media (max-width: 640px) {
  .sk-cards { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
