import { ref } from 'vue'
import type { Session } from './types'
import { ApiError, request } from './transport'
export { query, ApiError } from './transport'
export const session = ref<Session | null>(null)
export const clockOffset = ref(0)
// 页面时钟只用于显示与预检查；任务截止时间仍以服务端验证为准。
export function calibrate(serverTime?: string) {
  if (serverTime && Number.isFinite(Date.parse(serverTime)))
    clockOffset.value = Date.parse(serverTime) - Date.now()
}
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  // 只有会话过期才退回登录。403 保留会话，方便解释权限或 CSRF 错误。
  try {
    return await request<T>(path, init, session.value?.csrfToken ?? '')
  } catch (error) {
    if (error instanceof ApiError && error.httpStatus === 401) session.value = null
    throw error
  }
}
export async function restoreSession() {
  const value = await api<Session>('/api/session')
  session.value = value
  calibrate(value.serverTime)
}
export async function login(role: string, accessCode: string) {
  const value = await api<Session>('/api/session', {
    method: 'POST',
    body: JSON.stringify({ role, accessCode }),
  })
  session.value = value
  calibrate(value.serverTime)
}
export async function logout() {
  await api('/api/session', { method: 'DELETE' })
  session.value = null
}
export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : '请求未完成，请重试'
}
