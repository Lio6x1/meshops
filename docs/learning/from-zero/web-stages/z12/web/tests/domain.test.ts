import test from 'node:test'
import assert from 'node:assert/strict'
import {
  display,
  applyUpdate,
  newStreamState,
  isExpired,
  taskLabel,
  canCancel,
  makeDraft,
  searchExpired,
  canInspect,
} from '../src/domain.ts'

test('optional values retain unknown, real zero and false are facts', () => {
  assert.equal(display(undefined), '未知')
  assert.equal(display(0), '0')
  assert.equal(display(false), '否')
})
test('expiration is separate from stream connection and uses adjusted time', () => {
  assert.equal(isExpired('2026-01-01T00:00:01Z', Date.parse('2026-01-01T00:00:02Z')), true)
  assert.equal(isExpired(undefined, Date.now()), true)
})
test('subscription initializes only at snapshot end; reconnect drops old initialization', () => {
  let state = newStreamState()
  state = applyUpdate(state, {
    syncId: 'a',
    kind: 'ENTITY_UPDATE_KIND_SNAPSHOT',
    entityId: 'd',
    version: '9007199254740993',
    snapshot: { status: 'idle' },
  })
  assert.equal(state.ready, false)
  state = applyUpdate(state, { syncId: 'a', kind: 'ENTITY_UPDATE_KIND_SNAPSHOT_END' })
  assert.equal(state.ready, true)
  state = applyUpdate(state, {
    syncId: 'b',
    kind: 'ENTITY_UPDATE_KIND_SNAPSHOT',
    entityId: 'd',
    version: '1',
    snapshot: { status: 'busy' },
  })
  assert.equal(state.ready, false)
  assert.equal(state.entities.d?.version, '1')
})
test('versions compare losslessly and delete removes only latest entity', () => {
  let s = applyUpdate(newStreamState(), {
    syncId: 'a',
    kind: 'ENTITY_UPDATE_KIND_SNAPSHOT',
    entityId: 'd',
    version: '9007199254740993',
    snapshot: { status: 'idle' },
  })
  s = applyUpdate(s, {
    syncId: 'a',
    kind: 'ENTITY_UPDATE_KIND_UPSERT',
    entityId: 'd',
    version: '9007199254740992',
    snapshot: { status: 'old' },
  })
  assert.equal(s.entities.d?.snapshot?.status, 'idle')
  s = applyUpdate(s, {
    syncId: 'a',
    kind: 'ENTITY_UPDATE_KIND_DELETE',
    entityId: 'd',
    version: '9007199254740994',
  })
  assert.equal(s.entities.d, undefined)
  s = applyUpdate(s, {
    syncId: 'a',
    kind: 'ENTITY_UPDATE_KIND_UPSERT',
    entityId: 'd',
    version: '9007199254740993',
    snapshot: { status: 'late' },
  })
  assert.equal(s.entities.d, undefined)
})
test('cancel intent never claims execution has stopped', () => {
  assert.equal(
    taskLabel({ status: 'TASK_STATUS_EXECUTING', cancelRequested: true }),
    '执行中 · 已请求取消，等待确认',
  )
  assert.equal(canCancel({ status: 'TASK_STATUS_SUCCEEDED' }), false)
})
test('frozen create draft reuses identical payload and key after uncertain response', () => {
  const input = {
    entityId: 'drone-001',
    duration: 4,
    note: '巡检',
    deadline: '2026-09-13T00:00:00Z',
  }
  const draft = makeDraft(input, 'stable-key')
  input.note = 'changed'
  assert.equal(draft.idempotencyKey, 'stable-key')
  assert.equal(draft.payload.payloadJson, '{"duration_seconds":4,"note":"巡检"}')
  assert.ok(Object.isFrozen(draft))
  assert.ok(Object.isFrozen(draft.payload))
  assert.throws(() => makeDraft({ ...input, duration: 0 }, 'x'))
  assert.throws(() => makeDraft({ ...input, note: '中'.repeat(86) }, 'x'))
})
test('search expired cursor gets dedicated recovery rather than generic retry', () => {
  assert.equal(searchExpired({ code: 9 }), true)
  assert.equal(searchExpired({ code: 14 }), false)
})
test('inspection requires synchronized fresh granted capability and on-duty person', () => {
  const inventory = {
    entityId: 'p',
    entityType: 'person',
    executorId: 'e',
    supportedTasks: ['inspect'],
  }
  const record = {
    expiresAt: '2026-01-02T00:00:00Z',
    snapshot: {
      status: 'online',
      capability: { supportedTasks: ['inspect'] },
      person: { onDuty: true },
    },
  }
  const now = Date.parse('2026-01-01T00:00:00Z')
  assert.equal(canInspect(inventory, record, true, now), true)
  assert.equal(canInspect(inventory, record, false, now), false)
  assert.equal(
    canInspect(inventory, { ...record, snapshot: { ...record.snapshot, person: {} } }, true, now),
    false,
  )
  assert.equal(
    canInspect(
      inventory,
      { ...record, snapshot: { ...record.snapshot, status: 'busy' } },
      true,
      now,
    ),
    false,
  )
  assert.equal(canInspect({ ...inventory, supportedTasks: [] }, record, true, now), false)
})
