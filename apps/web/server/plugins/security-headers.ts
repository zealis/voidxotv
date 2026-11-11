// 页面级安全响应头（开发技术规范 §11.5）：CSP 必须在服务端写入，
// 不能用客户端 <meta> 替代。媒体域来自环境变量，生产指向 CDN。
const MEDIA_ORIGINS = (process.env.YJ_MEDIA_ORIGINS || 'http://127.0.0.1:9100 https:')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean)

// 开发期预览面板通过 iframe 嵌入页面，因此仅在业务环境为 production 时禁止嵌套。
// 注意不能只看 NODE_ENV：Nuxt 构建产物运行时 NODE_ENV 恒为 production。
const FRAME_ANCESTORS = process.env.YJ_ENV === 'production' ? "frame-ancestors 'none'" : ''

const CSP = [
  "default-src 'self'",
  "base-uri 'self'",
  "object-src 'none'",
  FRAME_ANCESTORS,
  "form-action 'self'",
  // Nuxt 的 hydration 载荷是内联脚本；生产可替换为 nonce 方案。
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  `img-src 'self' data: blob: ${MEDIA_ORIGINS.join(' ')}`,
  `media-src 'self' blob: ${MEDIA_ORIGINS.join(' ')}`,
  // blob: 是 hls.js 取 MSE playlist/分片的通道（播放器把签名 master 包成
  // Blob URL 再 fetch）；缺它整个 HLS 播放在浏览器里直接失败。
  `connect-src 'self' blob: ${MEDIA_ORIGINS.join(' ')}`,
  "font-src 'self' data:",
  "worker-src 'self' blob:",
].filter(Boolean).join('; ')

export default defineNitroPlugin((nitroApp) => {
  nitroApp.hooks.hook('render:response', (response) => {
    const headers = response.headers || (response.headers = {})
    headers['content-security-policy'] = CSP
    headers['x-content-type-options'] = 'nosniff'
    headers['referrer-policy'] = 'strict-origin-when-cross-origin'
    headers['permissions-policy'] = 'camera=(), microphone=(), geolocation=(), payment=()'
    if (process.env.NODE_ENV === 'production') {
      headers['strict-transport-security'] = 'max-age=31536000; includeSubDomains'
    }
  })
})
