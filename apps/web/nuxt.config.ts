// 夜街前端：预渲染为主、动态更新为辅（前端工程架构规范 V1.0.2 §4）
// Nuxt 4 默认 srcDir 为 app/，因此页面与样式放在 apps/web/app/ 下。

import { readFileSync } from 'node:fs'

// 版本号的事实来源是仓库根 VERSION；package.json 的 version 由
// `node scripts/bump-version.mjs` 同步，这里只读不写。
const { version: appVersion } = JSON.parse(
  readFileSync(new URL('./package.json', import.meta.url), 'utf8'),
) as { version: string }

// 公开媒体（匿名可读的 CDN 地址）可以安全使用长缓存；开发期播放地址是
// 30 分钟签名 URL，缓存必须短于签名 TTL，否则会出现「缓存了已过期地址」。
const mediaSWR = process.env.YJ_PUBLIC_MEDIA === 'true' ? 3600 : 300

export default defineNuxtConfig({
  compatibilityDate: '2026-11-06',
  devtools: { enabled: false },
  typescript: { strict: true, typeCheck: false },
  // 沉浸组件自研，目录名不作为组件名前缀，避免 <ImmersiveImIcon> 之类的嵌套命名。
  components: [
    { path: '~/components/immersive', pathPrefix: false },
    { path: '~/components/admin', pathPrefix: false },
    // 内容页通用基础组件（Vp*）：不加这行会按目录名加前缀，变成 <BaseVpSelect>。
    { path: '~/components/base', pathPrefix: false },
    // 创作者中心的工具组件（作品管理 / 评论管理）。
    { path: '~/components/studio', pathPrefix: false },
    // 发布页字段组件（话题标签 / 发布设置）。
    { path: '~/components/publish', pathPrefix: false },
    '~/components',
  ],
  css: ['~/assets/css/tokens.css'],

  runtimeConfig: {
    // 服务端读取；浏览器只拿到 public 下的非敏感值。
    apiOrigin: process.env.API_ORIGIN || 'http://127.0.0.1:18080',
    public: {
      siteUrl: process.env.YJ_SITE_URL || process.env.YJ_WEB_ORIGIN || 'http://127.0.0.1:3000',
      // 与 CHANGELOG.md 的版本条目同名；CI/发布可用 YJ_APP_VERSION 覆盖。
      appVersion: process.env.YJ_APP_VERSION || appVersion,
    },
  },

  routeRules: {
    // Nitro SWR 即规范所述「ISR + 后台刷新」：命中旧页面立即返回，后台再验证。
    // 游客态首页 60s；登录态在 M2 由 SSR 首屏 + 客户端个性化流承接，不进公共缓存。
    '/': { swr: 60 },
    // YJ_PUBLIC_MEDIA=true（生产 CDN 公共地址）时恢复规范要求的 3600s。
    '/video/**': { swr: mediaSWR },
    '/image/**': { swr: mediaSWR },
    '/creator/**': { swr: 300 },
    '/topic': { swr: 600 },
    '/topic/**': { swr: 600 },
    '/category/**': { swr: 600 },
    '/campaign/**': { swr: 3600 },
    // M3：SEO 端点由 Nuxt 服务端生成，缓存短于内容变更时效目标。
    '/sitemap.xml': { swr: 600 },
    '/sitemap-**': { swr: 600 },
    '/robots.txt': { swr: 3600 },
    '/internal/**': { headers: { 'Cache-Control': 'private, no-store' } },
    // 搜索为动态 SSR，通常不参与再生成。
    // 搜索为动态 SSR 且响应含个性化状态，禁止任何共享缓存（§9.2 / §10.6）。
    '/search': { ssr: true, cache: false, headers: { 'Cache-Control': 'private, no-store' } },
    '/login': { ssr: true, cache: false },
    // 私有页面禁止进入公共 CDN 缓存。
    '/me': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/me/**': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/library': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/messages': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/settings/**': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/studio/**': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/upload': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/publish': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    // 管理后台固定深色（Token 规范 §5.6），且为私有 SSR。
    '/admin': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
    '/admin/**': { ssr: true, headers: { 'Cache-Control': 'private, no-store' } },
  },

  app: {
    head: {
      htmlAttrs: { lang: 'zh-CN' },
      title: '夜街',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1, viewport-fit=cover' },
        { name: 'theme-color', content: '#0A0A1F' },
        { name: 'description', content: '夜街 · 沉浸式短视频' },
      ],
      // §14「主题初始化须在首屏绘制前完成」：同步内联脚本在 SSR 的 <head> 里
      // 先于任何绘制定好 html[data-tool-theme]。localStorage 存用户显式选择，
      // 缺失时按 system 跟随 prefers-color-scheme。只影响 [data-surface="tool"]，
      // 沉浸页与 admin 恒深色，读到此属性也不会变。
      script: [{
        type: 'text/javascript',
        innerHTML: `(function(){try{var t=localStorage.getItem('yj-tool-theme')||'system';` +
          `var m=window.matchMedia('(prefers-color-scheme: light)');` +
          `document.documentElement.setAttribute('data-tool-theme',` +
          `t==='light'||(t==='system'&&m.matches)?'light':'dark')}catch(e){` +
          `document.documentElement.setAttribute('data-tool-theme','dark')}})()`,
      }],
    },
  },

  nitro: {
    compressPublicAssets: true,
    // 登录 / 注册是静态壳（前端架构规范 §4.1）；详情页清单由 sitemap 在运行时提供，
    // 不在构建期拉取 API，避免 CI 无后端时构建失败。
    prerender: { routes: ['/login'] },
  },
})
