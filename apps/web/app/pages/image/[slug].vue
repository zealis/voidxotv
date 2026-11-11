<script setup lang="ts">
// 图集深链接落地页：SSR 输出全部图片与替代文本（UI 规范 §5.4 / 开发规范 §11）。
const route = useRoute()
const slug = computed(() => String(route.params.slug || ''))
</script>

<template>
  <ImmersiveDetail :slug="slug" expected-type="image" />
</template>
