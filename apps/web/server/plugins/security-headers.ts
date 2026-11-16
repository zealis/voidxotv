// 页面级安全响应头（开发技术规范 §11.5）：CSP 必须在服务端写入，
// 不能用客户端 <meta> 替代。媒体域来自环境变量，生产指向 CDN。
const MEDIA_ORIGINS = (process.env.YJ_MEDIA_ORIGINS || 'http://127.0.0.1:9100 https:')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean)

// KLIPY 是「浏览器直连」的 GIF 图源（它的条款要求请求由终端发起）：
// 请求要放行 api.klipy.com，结果图要放行它的媒体子域。
// 不依赖 YJ_MEDIA_ORIGINS 里那个 `https:` 兜底——生产可能把媒体源收敛成自有 CDN。
const KLIPY_ORIGINS = ['https://api.klipy.com', 'https://*.klipy.com']
const IMG_ORIGINS = [...MEDIA_ORIGINS, ...KLIPY_ORIGINS]
const CONNECT_ORIGINS = [...MEDIA_ORIGINS, 'https://api.klipy.com']

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
  `img-src 'self' data: blob: ${IMG_ORIGINS.join(' ')}`,
  // media-src 追加 https:：去中心化发布的视频直链可来自任意 https 源
  // （不落存储、按链接播放），媒体域白名单只覆盖本站自有媒体。
  `media-src 'self' blob: https: ${MEDIA_ORIGINS.join(' ')}`,
  // frame-src 不能省：缺省时回落 default-src 'self'，「下载原图」的隐藏
  // iframe 指向媒体域签名地址，会被静默拦截，点击看起来毫无反应。
  `frame-src 'self' ${MEDIA_ORIGINS.join(' ')}`,
  // blob: 是 hls.js 取 MSE playlist/分片的通道（播放器把签名 master 包成
  // Blob URL 再 fetch）；缺它整个 HLS 播放在浏览器里直接失败。
  `connect-src 'self' blob: ${CONNECT_ORIGINS.join(' ')}`,
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
    // 页面只允许同源读取：跨站即便拿到 URL 也读不到内容（防被第三方页面 fetch）。
    headers['cross-origin-resource-policy'] = 'same-origin'
    // 旧浏览器不认 CSP frame-ancestors，用 X-Frame-Options 兜底；开发期预览面板
    // 通过 iframe 嵌入页面，因此只在业务环境 production 下禁止嵌套。
    if (process.env.YJ_ENV === 'production') {
      headers['x-frame-options'] = 'DENY'
      headers['cross-origin-opener-policy'] = 'same-origin'
    }
    if (process.env.NODE_ENV === 'production') {
      headers['strict-transport-security'] = 'max-age=31536000; includeSubDomains'
    }
  })
})
