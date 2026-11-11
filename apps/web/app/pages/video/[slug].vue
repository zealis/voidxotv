<script setup lang="ts">
// 视频深链接落地页（UI 规范 §5.1：切换时同步 URL，刷新可恢复）。
const route = useRoute()
const slug = computed(() => String(route.params.slug || ''))
</script>

<template>
  <ImmersiveDetail :slug="slug" expected-type="video" />
</template>
