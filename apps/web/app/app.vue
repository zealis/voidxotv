<script setup lang="ts">
// 全站 WebSite + SearchAction 结构化数据（前端架构规范 §5.1）。
const requestURL = useRequestURL()

// 偏好只在客户端读一次：未登录时 load() 会安静退出。
// 路由切换时重算浏览器 UI 颜色：工具页浅色、沉浸页恒深色（§14）。
const { load: loadPreferences, applyTheme } = usePreferences()
const route = useRoute()
onMounted(() => { void loadPreferences() })
watch(() => route.fullPath, () => { applyTheme() })
useHead(() => ({
  script: [{
    type: 'application/ld+json',
    innerHTML: JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'WebSite',
      name: '夜街',
      url: requestURL.origin,
      potentialAction: {
        '@type': 'SearchAction',
        target: `${requestURL.origin}/search?q={search_term_string}`,
        'query-input': 'required name=search_term_string',
      },
    }),
  }],
}))
</script>

<template>
  <NuxtPage />
  <!-- 登录 / 注册弹窗挂在这里：前台任何页面都能唤起，不打断当前内容。 -->
  <VpAuthDialog />
</template>

<style>
/* 「通用 → 减少动效」：把过渡与动画压到近乎瞬时，不影响状态本身。 */
html.vp-reduce-motion *,
html.vp-reduce-motion *::before,
html.vp-reduce-motion *::after {
  animation-duration: 0.001ms !important;
  animation-iteration-count: 1 !important;
  transition-duration: 0.001ms !important;
  scroll-behavior: auto !important;
}

html,
body,
#__nuxt {
  height: 100%;
  margin: 0;
  background: var(--vp-im-bg, #0a0a1f);
  color: #fff;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC',
    'Hiragino Sans GB', 'Microsoft YaHei', sans-serif;
}
</style>
