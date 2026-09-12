import type { EntityRecord, EntityUpdate, Task, Inventory } from './types.ts'
export const entityTypes: Record<string, string> = {
  person: '人员',
  drone: '无人机',
  vehicle: '地面车辆',
  robot: '巡检机器人',
  sensor: '固定传感器',
  facility: '设施',
}
export const statuses: Record<string, string> = {
  TASK_STATUS_CREATED: '已创建',
  TASK_STATUS_DISPATCH_PENDING: '等待分发',
  TASK_STATUS_DISPATCHED: '已分发',
  TASK_STATUS_ACKED: '已接收',
  TASK_STATUS_EXECUTING: '执行中',
  TASK_STATUS_SUCCEEDED: '已完成',
  TASK_STATUS_FAILED: '失败',
  TASK_STATUS_CANCELLED: '已取消',
  TASK_STATUS_TIMED_OUT: '已超时',
  TASK_STATUS_REJECTED: '已拒绝',
}
export function display(value: unknown): string {
  return value === undefined || value === null
    ? '未知'
    : typeof value === 'boolean'
      ? value
        ? '是'
        : '否'
      : String(value)
}
export function time(value?: string): string {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
}
export function isExpired(expiresAt: string | undefined, now: number): boolean {
  return !expiresAt || Date.parse(expiresAt) <= now
}
export function canInspect(
  binding: Inventory | undefined,
  record: EntityRecord | undefined,
  ready: boolean,
  now: number,
): boolean {
  const snapshot = record?.snapshot
  return !!(
    ready &&
    binding?.executorId &&
    binding.supportedTasks?.includes('inspect') &&
    snapshot &&
    !isExpired(record?.expiresAt, now) &&
    !['offline', 'busy', 'fault'].includes(snapshot.status ?? '') &&
    (!snapshot.person || snapshot.person.onDuty) &&
    snapshot.capability?.supportedTasks?.includes('inspect')
  )
}
export function taskLabel(task: Task): string {
  return (
    (statuses[task.status ?? ''] ?? '未知') +
    (task.cancelRequested && canCancel(task) ? ' · 已请求取消，等待确认' : '')
  )
}
export function canCancel(task: Task): boolean {
  return (
    !!task.status &&
    !['SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'REJECTED'].some(
      (v) => task.status === 'TASK_STATUS_' + v,
    )
  )
}
export function searchExpired(error: unknown): boolean {
  return !!error && typeof error === 'object' && 'code' in error && error.code === 9
}
export interface StreamState {
  syncId: string
  generation: string
  ready: boolean
  entities: Record<string, EntityRecord>
  versions: Record<string, string>
}
export function newStreamState(): StreamState {
  return { syncId: '', generation: '', ready: false, entities: {}, versions: {} }
}
// An SSE reconnection is a new snapshot synchronization, never an offset resume.
export function applyUpdate(current: StreamState, update: EntityUpdate): StreamState {
  let state = { ...current, entities: { ...current.entities }, versions: { ...current.versions } }
  if (update.syncId && update.syncId !== state.syncId)
    state = { ...newStreamState(), syncId: update.syncId }
  if (update.viewGeneration && state.generation && update.viewGeneration !== state.generation)
    state = { ...newStreamState(), syncId: update.syncId ?? '' }
  if (update.viewGeneration) state.generation = update.viewGeneration
  if (update.kind === 'ENTITY_UPDATE_KIND_SNAPSHOT_END') {
    state.ready = true
    return state
  }
  if (update.kind === 'ENTITY_UPDATE_KIND_HEARTBEAT' || !update.entityId) return state
  // Keep a small per-subscribed-ID tombstone so delayed upserts cannot resurrect deletes.
  const previousVersion = state.versions[update.entityId]
  if (previousVersion && update.version && BigInt(update.version) <= BigInt(previousVersion))
    return state
  if (update.version) state.versions[update.entityId] = update.version
  if (update.kind === 'ENTITY_UPDATE_KIND_DELETE') delete state.entities[update.entityId]
  else state.entities[update.entityId] = { ...update, found: true }
  return state
}
export interface DraftInput {
  entityId: string
  duration: number
  note: string
  deadline: string
}
export function makeDraft(input: DraftInput, key: string) {
  if (
    !input.entityId ||
    !Number.isInteger(input.duration) ||
    input.duration < 1 ||
    input.duration > 60
  )
    throw new Error('请选择执行实体，时长为 1–60 秒整数')
  if (new TextEncoder().encode(input.note).length > 256)
    throw new Error('备注不能超过 256 UTF-8 字节')
  if (!Number.isFinite(Date.parse(input.deadline))) throw new Error('请选择有效截止时间')
  return Object.freeze({
    idempotencyKey: key,
    taskType: 'inspect',
    targetEntityId: input.entityId,
    payload: Object.freeze({
      payloadJson: JSON.stringify({ duration_seconds: input.duration, note: input.note }),
    }),
    priority: 5,
    deadline: new Date(input.deadline).toISOString(),
  })
}
