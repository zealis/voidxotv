// 夜街 · 同源 BFF 代理（前端工程架构规范 §2 / §11）
// 浏览器只与 Nuxt 同源通信：Cookie、CSRF 头与错误 Envelope 原样透传，
// 不把 Go API 暴露到公网，也不在浏览器侧处理跨域凭据。
// 请求体必须按二进制 Buffer 转发：图集上传是 multipart/form-data，
// 默认按 UTF-8 字符串解码会破坏 PNG 字节导致后端拒绝（§8.1 图集发布）。
// 大视频仍由浏览器直连对象存储，不经过这里。
const SKIP_REQUEST_HEADERS = new Set([
  'host', 'connection', 'content-length', 'transfer-encoding',
  'accept-encoding', 'keep-alive', 'proxy-authorization', 'te', 'trailer', 'upgrade',
])
const SKIP_RESPONSE_HEADERS = new Set([
  'content-encoding', 'content-length', 'transfer-encoding',
  'connection', 'keep-alive', 'set-cookie',
])

export default defineEventHandler(async (event) => {
  const origin = process.env.API_ORIGIN || 'http://127.0.0.1:18080'
  const url = getRequestURL(event)
  const target = `${origin}${url.pathname}${url.search}`
  const method = event.node.req.method || 'GET'

  const headers = new Headers()
  for (const [name, raw] of Object.entries(getRequestHeaders(event))) {
    if (raw === undefined || SKIP_REQUEST_HEADERS.has(name.toLowerCase())) continue
    headers.set(name, Array.isArray(raw) ? raw.join(', ') : String(raw))
  }

  // false = 返回 Buffer，而不是默认按 UTF-8 解码的字符串；二进制请求体原样透传。
  const rawBody = method === 'GET' || method === 'HEAD'
    ? undefined
    : await readRawBody(event, false)
  // Node 的 fetch 运行时接受 Buffer，但 DOM 的 BodyInit 类型不含它，需显式断言。
  const body = rawBody as unknown as BodyInit | undefined

  let upstream: Response
  try {
    upstream = await fetch(target, { method, headers, body, redirect: 'manual' })
  } catch (error) {
    setResponseStatus(event, 502, 'Bad Gateway')
    setResponseHeader(event, 'cache-control', 'no-store')
    return {
      error: {
        code: 'api_unreachable',
        message: `Go API 不可达：${error instanceof Error ? error.message : String(error)}`,
      },
    }
  }

  setResponseStatus(event, upstream.status, upstream.statusText)
  for (const [name, value] of upstream.headers.entries()) {
    if (!SKIP_RESPONSE_HEADERS.has(name.toLowerCase())) {
      setResponseHeader(event, name, value)
    }
  }
  const cookieHeaders = (upstream.headers as Headers & { getSetCookie?: () => string[] }).getSetCookie?.() || []
  for (const cookie of cookieHeaders) {
    appendResponseHeader(event, 'set-cookie', cookie)
  }
  setResponseHeader(event, 'cache-control', 'no-store')
  return Buffer.from(await upstream.arrayBuffer())
})
