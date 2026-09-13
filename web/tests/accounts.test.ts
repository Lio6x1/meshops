import test from 'node:test'
import assert from 'node:assert/strict'
import * as accountApi from '../src/api.ts'

const signedIn = {
  role: 'admin' as const, tenantId: 'demo', actorId: 'user-1', csrfToken: 'csrf-1',
  expiresAt: '2026-09-14T00:00:00Z', username: 'admin', displayName: '管理员',
  mustChangePassword: false,
}

test('personal login sends username and exact password without caller-selected role', async (t) => {
  let sent: RequestInit | undefined
  t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
    assert.equal(url, '/api/session')
    sent = init
    return Response.json(signedIn)
  })
  await accountApi.login('admin', '  exact-password  ')
  assert.deepEqual(JSON.parse(String(sent?.body)), { username: 'admin', password: '  exact-password  ' })
  assert.equal(accountApi.session.value?.username, 'admin')
  assert.equal(accountApi.session.value?.mustChangePassword, false)
  accountApi.session.value = null
})

test('password change uses CSRF and clears session only after server success', async (t) => {
  accountApi.session.value = signedIn
  t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
    assert.equal(url, '/api/account/password')
    assert.equal(init.method, 'POST')
    assert.equal(new Headers(init.headers).get('X-CSRF-Token'), 'csrf-1')
    assert.deepEqual(JSON.parse(String(init.body)), { currentPassword: 'old-password-1', newPassword: 'new-password-2' })
    return new Response(null, { status: 204 })
  })
  await accountApi.changePassword('old-password-1', 'new-password-2')
  assert.equal(accountApi.session.value, null)
})

test('failed password change retains session for correction', async (t) => {
  accountApi.session.value = signedIn
  t.mock.method(globalThis, 'fetch', async () => Response.json({ message: '当前密码不正确' }, { status: 400 }))
  await assert.rejects(accountApi.changePassword('wrong-password', 'new-password-2'))
  assert.equal(accountApi.session.value?.username, 'admin')
  accountApi.session.value = null
})

test('account management emits bounded cursor queries and operator-only mutation payloads', async (t) => {
  const calls: { path: string; method: string; body: unknown }[] = []
  accountApi.session.value = signedIn
  t.mock.method(globalThis, 'fetch', async (path: string, init: RequestInit) => {
    assert.equal(init.credentials, 'same-origin')
    if (init.method) assert.equal(new Headers(init.headers).get('X-CSRF-Token'), 'csrf-1')
    calls.push({ path, method: init.method ?? 'GET', body: init.body ? JSON.parse(String(init.body)) : null })
    return path.endsWith('reset-password') ? new Response(null, { status: 204 }) : Response.json({})
  })
  await accountApi.listAccounts('user+2/=')
  await accountApi.createAccount({ username: 'operator_1', displayName: '值班员', password: 'initial-password' })
  await accountApi.setAccountEnabled('user/2', false)
  await accountApi.resetAccountPassword('user/2', 'reset-password-1')
  assert.deepEqual(calls, [
    { path: '/api/accounts?limit=20&after=user%2B2%2F%3D', method: 'GET', body: null },
    { path: '/api/accounts', method: 'POST', body: { username: 'operator_1', displayName: '值班员', password: 'initial-password' } },
    { path: '/api/accounts/user%2F2', method: 'PATCH', body: { enabled: false } },
    { path: '/api/accounts/user%2F2/reset-password', method: 'POST', body: { password: 'reset-password-1' } },
  ])
  accountApi.session.value = null
})

test('permission errors keep the current session and authentication expiry clears it', async (t) => {
  accountApi.session.value = signedIn
  t.mock.method(globalThis, 'fetch', async () => Response.json({ message: '无权限' }, { status: 403 }))
  await assert.rejects(accountApi.listAccounts())
  assert.equal(accountApi.session.value?.username, 'admin')
  t.mock.method(globalThis, 'fetch', async () => Response.json({ message: '会话失效' }, { status: 401 }))
  await assert.rejects(accountApi.listAccounts())
  assert.equal(accountApi.session.value, null)
})

test('late authentication error from an old account cannot clear a newly signed-in session', async (t) => {
  accountApi.session.value = signedIn
  let release!: (response: Response) => void
  t.mock.method(globalThis, 'fetch', () => new Promise<Response>(resolve => { release = resolve }))
  const pending = accountApi.listAccounts()
  accountApi.session.value = { ...signedIn, username: 'next-admin', csrfToken: 'csrf-2' }
  release(Response.json({ message: '旧会话过期' }, { status: 401 }))
  await assert.rejects(pending)
  assert.equal(accountApi.session.value?.username, 'next-admin')
  accountApi.session.value = null
})
