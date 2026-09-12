import test from 'node:test'
import assert from 'node:assert/strict'
import { request, ApiError, query } from '../src/transport.ts'
test('HTTP errors retain gRPC code for recovery; non-JSON failures are bounded', async () => {
  await assert.rejects(
    request(
      '/api/x',
      {},
      '',
      async () => new Response('{"code":9,"message":"expired"}', { status: 400 }),
    ),
    (e) => e instanceof ApiError && e.code === 9,
  )
  await assert.rejects(
    request(
      '/api/x',
      {},
      '',
      async () => new Response('<html>secret debug</html>', { status: 502 }),
    ),
    (e) => e instanceof ApiError && !e.message.includes('secret'),
  )
})
test('write adds CSRF and JSON, cookie stays same-origin and errors propagate', async () => {
  let captured: RequestInit | undefined
  const response = await request(
    '/api/x',
    { method: 'POST', body: JSON.stringify({ reason: 'review' }) },
    'csrf',
    async (_url, init) => {
      captured = init
      return new Response('{}')
    },
  )
  assert.deepEqual(response, {})
  assert.equal(new Headers(captured?.headers).get('X-CSRF-Token'), 'csrf')
  assert.equal(captured?.credentials, 'same-origin')
})
test('queries preserve signed opaque cursors and omit empty filters', () => {
  assert.equal(
    query({ page_token: 'a+b/==', status: '', page_size: 20 }),
    '?page_token=a%2Bb%2F%3D%3D&page_size=20',
  )
})
