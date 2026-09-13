import type { Account, Session } from './types.ts'

type AccountSession = Pick<Session, 'role' | 'mustChangePassword'> | null
export function canUseBusiness(value: AccountSession): boolean {
  return !!value && !value.mustChangePassword
}
export function canManageAccount(value: AccountSession, account: Pick<Account, 'role'>): boolean {
  return canUseBusiness(value) && value?.role === 'admin' && account.role === 'operator'
}
export function usernameError(value: string): string {
  return /^[a-z0-9_-]{3,32}$/.test(value) ? '' : '用户名须为 3–32 位小写字母、数字、下划线或短横线'
}
export function passwordError(value: string): string {
  // 拒绝未配对代理项，避免浏览器把它替换为其他字符后提交；不裁剪密码。
  if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value))
    return '密码包含无效字符，请重新输入'
  const count = Array.from(value).length
  return count >= 12 && count <= 128 && new TextEncoder().encode(value).length <= 512
    ? '' : '密码须为 12–128 个字符，且不超过 512 字节'
}
