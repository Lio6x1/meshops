import { ref } from 'vue'
import type { Account, Session } from './types.ts'
import { ApiError, query, request } from './transport.ts'
export { query, ApiError } from './transport.ts'
export const session = ref<Session | null>(null)
export const clockOffset = ref(0)
// 页面时钟只用于显示与预检查；任务截止时间仍以服务端验证为准。
export function calibrate(serverTime?: string) {
  if (serverTime && Number.isFinite(Date.parse(serverTime)))
    clockOffset.value = Date.parse(serverTime) - Date.now()
}
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  // 只有会话过期才退回登录。403 保留会话，方便解释权限或 CSRF 错误。
  const requestedSession = session.value
  try {
    return await request<T>(path, init, requestedSession?.csrfToken ?? '')
  } catch (error) {
    // 旧账号的延迟响应不能清除用户后来建立的新会话。
    if (error instanceof ApiError && error.httpStatus === 401 && session.value === requestedSession)
      session.value = null
    throw error
  }
}
export async function restoreSession() {
  const value = await api<Session>('/api/session')
  session.value = value
  calibrate(value.serverTime)
}
export async function login(username: string, password: string) {
  const value = await api<Session>('/api/session', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
  session.value = value
  calibrate(value.serverTime)
}
export async function logout() {
  await api('/api/session', { method: 'DELETE' })
  session.value = null
}
export async function changePassword(currentPassword: string, newPassword: string) {
  await api('/api/account/password', {
    method: 'POST', body: JSON.stringify({ currentPassword, newPassword }),
  })
  session.value = null
}
export function listAccounts(after = '') {
  return api<{ accounts: Account[]; nextCursor?: string }>('/api/accounts' + query({ limit: 20, after }))
}
export function createAccount(input: { username: string; displayName: string; password: string }) {
  return api<Account>('/api/accounts', { method: 'POST', body: JSON.stringify(input) })
}
export function setAccountEnabled(id: string, enabled: boolean) {
  return api<Account>('/api/accounts/' + encodeURIComponent(id), {
    method: 'PATCH', body: JSON.stringify({ enabled }),
  })
}
export function resetAccountPassword(id: string, password: string) {
  return api<void>('/api/accounts/' + encodeURIComponent(id) + '/reset-password', {
    method: 'POST', body: JSON.stringify({ password }),
  })
}
export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : '请求未完成，请重试'
}
