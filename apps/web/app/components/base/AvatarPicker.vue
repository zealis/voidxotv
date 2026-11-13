<script setup lang="ts">
// 「随机头像 6 选 1」的候选区：只负责生成 seed、预览与选中，不负责弹窗壳和保存。
// 前台设置页用 VpDialog 包、后台用户管理用 ElDialog 包，两边共用同一份逻辑。
//
// 预览走同源 /api/v1/avatar/preview：前端 CSP 只允许 'self' 与自有媒体源，
// 直接把 dicebear.com 放进 <img src> 会被拦。选中后才由后端下载落库。
const props = defineProps<{ selected: string }>()
const emit = defineEmits<{ select: [seed: string] }>()

const seeds = ref<string[]>([])

function makeSeeds(count = 6): string[] {
  return Array.from({ length: count }, () => {
    const bytes = new Uint8Array(12)
    crypto.getRandomValues(bytes)
    return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  })
}

function reshuffle() {
  seeds.value = makeSeeds()
  // 换一批后旧的选择不再有效，让确认按钮回到禁用态。
  emit('select', '')
}

const preview = (seed: string) => `/api/v1/avatar/preview?seed=${seed}`

onMounted(reshuffle)
defineExpose({ reshuffle })
</script>

<template>
  <div class="avatar-picker">
    <p class="tip">从 6 张随机头像里挑一张，选定后会保存到账号。</p>
    <div class="grid">
      <button
        v-for="seed in seeds" :key="seed" type="button"
        class="candidate" :class="{ on: props.selected === seed }"
        :aria-pressed="props.selected === seed" :aria-label="`选择随机头像 ${seed.slice(0, 6)}`"
        @click="emit('select', seed)"
      >
        <img :src="preview(seed)" alt="" loading="lazy" />
        <span v-if="props.selected === seed" class="tick" aria-hidden="true">✓</span>
      </button>
    </div>
    <button type="button" class="shuffle" @click="reshuffle">换一批</button>
  </div>
</template>

<style scoped>
.avatar-picker { display: grid; gap: 12px; }
.tip {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--vp-tool-text-2, #a1a1aa);
}
.grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.candidate {
  position: relative;
  aspect-ratio: 1;
  padding: 0;
  border: 2px solid var(--vp-tool-dialog-border, rgba(255, 255, 255, 0.14));
  border-radius: 12px;
  overflow: hidden;
  background: var(--vp-tool-input, rgba(255, 255, 255, 0.04));
  cursor: pointer;
  transition: border-color 160ms ease, transform 160ms ease;
}
.candidate:hover { transform: translateY(-2px); }
.candidate.on { border-color: var(--vp-color-brand, #ff2e7e); }
.candidate img { width: 100%; height: 100%; object-fit: cover; display: block; }
.tick {
  position: absolute;
  right: 6px;
  bottom: 6px;
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: var(--vp-color-brand, #ff2e7e);
  color: #fff;
  font-size: 13px;
  font-weight: 700;
}
.shuffle {
  justify-self: end;
  border: 0;
  background: none;
  padding: 4px 2px;
  cursor: pointer;
  font-size: 12px;
  color: var(--vp-tool-accent-ink, #ff77b0);
  text-decoration: underline;
}
</style>
