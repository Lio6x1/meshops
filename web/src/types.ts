export interface Session {
  role: 'operator' | 'admin'
  username: string
  displayName: string
  mustChangePassword: boolean
  tenantId: string
  actorId: string
  csrfToken: string
  expiresAt: string
  serverTime?: string
}
export interface Account {
  id: string
  username: string
  displayName: string
  role: 'operator' | 'admin'
  enabled: boolean
  mustChangePassword: boolean
  createdAt: string
  updatedAt: string
}
export interface Inventory {
  entityId: string
  entityType: string
  executorId?: string
  supportedTasks?: string[]
}
export interface Snapshot {
  entityType?: string
  status?: string
  capability?: { supportedTasks?: string[] }
  location?: { latitude?: number; longitude?: number; altitude?: number; accuracy?: number }
  velocity?: { speed?: number; heading?: number; verticalSpeed?: number }
  power?: { batteryPercent?: number }
  person?: { onDuty?: boolean; availability?: string; skills?: string[] }
  vehicle?: { loadKg?: number; availability?: string }
  robot?: { faultCodes?: string[]; availability?: string }
  sensor?: { reading?: number; unit?: string; measuredAt?: string; health?: string }
  facility?: {
    facilityType?: string
    isOpen?: boolean
    occupiedSlots?: number
    totalSlots?: number
  }
}
export interface EntityRecord {
  entityId?: string
  version?: string
  snapshot?: Snapshot
  updatedAt?: string
  expiresAt?: string
  sourceId?: string
  sourceGeneration?: string
  viewGeneration?: string
  executorId?: string
  found?: boolean
}
export interface EntityUpdate extends EntityRecord {
  kind?: string
  syncId?: string
}
export interface Task {
  taskId?: string
  targetEntityId?: string
  taskType?: string
  status?: string
  statusVersion?: number
  createdAt?: string
  updatedAt?: string
  deadline?: string
  executorId?: string
  executionKey?: string
  cancelRequested?: boolean
  cancelledReason?: string
  failureReason?: string
  resultJson?: string
  payload?: { payloadJson?: string }
  note?: string
}
export interface TaskChange {
  fromStatus?: string
  toStatus?: string
  changedAt?: string
  changedBy?: string
  reason?: string
  statusVersion?: number
  eventId?: string
}
export interface Dispatch {
  taskId?: string
  dispatchId?: string
  attempt?: number
  status?: string
  executorId?: string
  dispatchedAt?: string
  ackedAt?: string
  completedAt?: string
  commandId?: string
  executionKey?: string
  deliveryStatus?: string
  commandKind?: string
}
export interface HistorySample {
  sampleId?: string
  eventId?: string
  occurredAt?: string
  sampledAt?: string
  sampleReason?: string
  snapshot?: Snapshot
}
