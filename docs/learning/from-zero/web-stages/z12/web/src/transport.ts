export class ApiError extends Error {
  code: number
  httpStatus: number
  constructor(message: string, code: number, status: number) {
    super(message)
    this.code = code
    this.httpStatus = status
  }
}
export async function request<T>(
  path: string,
  init: RequestInit = {},
  csrf = '',
  fetcher: typeof fetch = fetch,
): Promise<T> {
  // Cookie 由浏览器管理。页面只能传 CSRF 值，不能读取 HttpOnly 会话或后端机器令牌。
  const headers = new Headers(init.headers)
  if (init.body) headers.set('Content-Type', 'application/json')
  if (init.method && !['GET', 'HEAD'].includes(init.method)) headers.set('X-CSRF-Token', csrf)
  const response = await fetcher(path, { ...init, headers, credentials: 'same-origin' })
  const data = await response.json().catch(() => null)
  // 代理可能返回 HTML 错误页，不能把其内部诊断原样展示给业务用户。
  if (!response.ok)
    throw new ApiError(
      data?.message ?? `请求失败（HTTP ${response.status}），请检查网关日志`,
      Number(data?.code ?? 0),
      response.status,
    )
  return data as T
}
export function query(values: Record<string, string | number | undefined>): string {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(values))
    if (v !== undefined && v !== '') params.set(k, String(v))
  return params.size ? '?' + params.toString() : ''
}
