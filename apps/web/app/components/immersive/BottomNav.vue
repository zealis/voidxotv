<script setup lang="ts">
// 底部主导航（UI 规范 §6.1）：首页 / 发现 / 发布 / 消息 / 我的。
// 从 ChromeBars 抽出来，供沉浸流与普通工具页（消息中心、「我」）共用——
// 这些页面的顶部是各自的标题栏，不该出现沉浸流的「关注 / 推荐」频道切换。
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  // 收起状态：视频播放中、图片浏览态。只在 PC 端（≥781px）生效，移动端始终常驻。
  collapsed?: boolean
}>(), {
  collapsed: false,
})

const route = useRoute()
// 未读消息：底栏「消息」右上角的小粉点（不复用计数文案，避免干扰导航）。
const { count: unread } = useUnread()

// 按路由前缀高亮，子页（/me/relations、/me/drafts）仍归属「我的」。
const active = computed(() => {
  const path = route.path
  if (path === '/') return 'home'
  if (path.startsWith('/search') || path.startsWith('/category') || path.startsWith('/topic')) return 'discover'
  if (path.startsWith('/messages')) return 'messages'
  if (path.startsWith('/me')) return 'me'
  return ''
})
</script>

<template>
  <nav
    class="bottom-nav vp-glass"
    :class="{ 'bottom-nav--auto-hide': props.collapsed }"
    aria-label="主导航"
  >
    <NuxtLink
      to="/" class="nav-item" :class="{ active: active === 'home' }"
      :aria-current="active === 'home' ? 'page' : undefined" aria-label="首页"
    >
      <ImIcon name="home" :size="22" /><span>首页</span>
    </NuxtLink>
    <NuxtLink
      to="/search" class="nav-item" :class="{ active: active === 'discover' }"
      :aria-current="active === 'discover' ? 'page' : undefined" aria-label="发现"
    >
      <ImIcon name="discover" :size="22" /><span>发现</span>
    </NuxtLink>
    <NuxtLink to="/publish" class="nav-publish" aria-label="发布作品">
      <ImIcon name="plus" :size="26" />
    </NuxtLink>
    <NuxtLink
      to="/messages" class="nav-item" :class="{ active: active === 'messages' }"
      :aria-current="active === 'messages' ? 'page' : undefined" aria-label="消息"
    >
      <ImIcon name="message" :size="22" /><span>消息</span>
      <span v-if="unread > 0" class="dot" aria-hidden="true" />
    </NuxtLink>
    <NuxtLink
      to="/me" class="nav-item" :class="{ active: active === 'me' }"
      :aria-current="active === 'me' ? 'page' : undefined" aria-label="我的"
    >
      <ImIcon name="user" :size="22" /><span>我的</span>
    </NuxtLink>
  </nav>
</template>

<style scoped>
.bottom-nav {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: var(--vp-im-z-controls);
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  align-items: center;
  padding: 8px 8px calc(8px + env(safe-area-inset-bottom));
  border-width: 1px 0 0;
  border-radius: 0;
}
/* 未读粉点：挂在「消息」图标右上。用品牌强调色，直径 7px——
   再小在 1x 屏上看不见，再大就成了「角标」而不是提示。 */
.nav-item .dot {
  position: absolute;
  /* 图标 22px：右边缘在 50%+11，圆点骑在这条边上（再往左就压住图形笔画）。 */
  top: 4px;
  left: calc(50% + 8px);
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--vp-im-accent);
  /* 与底栏玻璃底同色的细描边，避免落在图标笔画上糊成一团。 */
  box-shadow: 0 0 0 2px rgba(18, 18, 32, 0.9);
}

/* PC 端：视频播放中收起底栏，暂停后恢复。移动端（≤780px）不匹配这条规则，
   底栏保持常驻，控制条也本来就在移动端隐藏，两者互不干扰。 */
@media (min-width: 781px) {
  .bottom-nav {
    transition: opacity 200ms ease, transform 200ms ease, visibility 0s;
  }
  .bottom-nav--auto-hide {
    opacity: 0;
    transform: translateY(100%);
    visibility: hidden;
    pointer-events: none;
    transition: opacity 200ms ease, transform 200ms ease, visibility 0s linear 200ms;
  }
}
.nav-item {
  position: relative;
  display: grid;
  justify-items: center;
  gap: 2px;
  min-height: 44px;
  color: var(--vp-im-text-secondary);
  font-size: 11px;
  text-decoration: none;
}
.nav-item.active { color: var(--vp-im-text-primary); }
.nav-publish {
  display: grid;
  place-items: center;
  width: 46px;
  height: 34px;
  margin: 0 auto;
  border-radius: 11px;
  color: var(--vp-im-on-accent);
  background: linear-gradient(120deg, var(--vp-im-accent), #a03bff);
  box-shadow: 0 6px 18px rgba(255, 46, 126, 0.35);
}
</style>
