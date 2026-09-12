import test from 'node:test'
import assert from 'node:assert/strict'
import { LatestRequest } from '../src/requests.ts'
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}
test('late search response cannot replace newer filtered results or cursor', async () => {
  const gate = new LatestRequest(),
    first = deferred<{ rows: string[]; cursor: string }>(),
    second = deferred<{ rows: string[]; cursor: string }>()
  let rows: string[] = [],
    cursor = '',
    finishes = 0
  const apply = (value: { rows: string[]; cursor: string }) => {
    rows = value.rows
    cursor = value.cursor
  }
  const a = gate.run(
    () => first.promise,
    apply,
    () => assert.fail('unexpected failure'),
    () => finishes++,
  )
  const b = gate.run(
    () => second.promise,
    apply,
    () => assert.fail('unexpected failure'),
    () => finishes++,
  )
  second.resolve({ rows: ['new-filter'], cursor: 'new-token' })
  await b
  first.resolve({ rows: ['old-filter'], cursor: 'old-token' })
  await a
  assert.deepEqual(rows, ['new-filter'])
  assert.equal(cursor, 'new-token')
  assert.equal(finishes, 1)
})
test('changing selected entity discards both old history results and old errors', async () => {
  const gate = new LatestRequest(),
    old = deferred<string[]>()
  let rows: string[] = [],
    error = ''
  let finish = 0
  const running = gate.run(
    () => old.promise,
    (v) => {
      rows = v
    },
    (e) => {
      error = String(e)
    },
    () => finish++,
  )
  gate.invalidate()
  old.reject(new Error('old entity failed'))
  await running
  assert.deepEqual(rows, [])
  assert.equal(error, '')
  assert.equal(finish, 0)
  await gate.run(
    async () => ['new entity sample'],
    (v) => {
      rows = v
    },
    () => assert.fail(),
    () => finish++,
  )
  assert.deepEqual(rows, ['new entity sample'])
  assert.equal(finish, 1)
})
