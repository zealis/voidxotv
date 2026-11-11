// canonical / Open Graph 需要绝对地址。构建期默认值会把端口写死，
// 因此以运行时配置 NUXT_PUBLIC_SITE_URL（由 YJ_SITE_URL 派生）为准；
// 未配置时才按请求头（CDN 的 x-forwarded-host → Host）推导。
export function useSiteOrigin(): string {
  const config = useRuntimeConfig()
  const configured = String(config.public.siteUrl || '').replace(/\/$/, '')
  if (configured) return configured
  const headers = useRequestHeaders(['x-forwarded-host', 'host', 'x-forwarded-proto'])
  const host = headers['x-forwarded-host'] || headers.host
  if (!host) return ''
  const proto = headers['x-forwarded-proto'] || 'https'
  return `${proto}://${host}`
}
