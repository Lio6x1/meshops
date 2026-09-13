import test from 'node:test'
import assert from 'node:assert/strict'
import { canManageAccount, canUseBusiness, passwordError, usernameError } from '../src/accounts.ts'

test('forced password change blocks business even for admin; only admin manages operators', () => {
  assert.equal(canUseBusiness(null), false)
  assert.equal(canUseBusiness({ role: 'admin', mustChangePassword: true }), false)
  assert.equal(canUseBusiness({ role: 'operator', mustChangePassword: false }), true)
  assert.equal(canManageAccount({ role: 'admin', mustChangePassword: false }, { role: 'operator' }), true)
  assert.equal(canManageAccount({ role: 'admin', mustChangePassword: false }, { role: 'admin' }), false)
  assert.equal(canManageAccount({ role: 'operator', mustChangePassword: false }, { role: 'operator' }), false)
  assert.equal(canManageAccount({ role: 'admin', mustChangePassword: true }, { role: 'operator' }), false)
})

test('password policy counts Unicode characters and never silently trims or replaces invalid UTF-16', () => {
  assert.equal(passwordError('a'.repeat(8)), '')
  assert.equal(passwordError('🔐'.repeat(128)), '')
  assert.equal(passwordError(' 1234567890 '), '')
  assert.notEqual(passwordError('🔐'.repeat(7)), '')
  assert.notEqual(passwordError('a'.repeat(129)), '')
  assert.notEqual(passwordError('a'.repeat(12) + '\uD800'), '')
})

test('usernames enforce the account contract without silently normalizing identities', () => {
  assert.equal(usernameError('ops_01-test'), '')
  assert.equal(usernameError('a'.repeat(32)), '')
  for (const name of ['ab', 'a'.repeat(33), 'Admin', ' ops ', '操作员', 'ops.name'])
    assert.notEqual(usernameError(name), '')
})
