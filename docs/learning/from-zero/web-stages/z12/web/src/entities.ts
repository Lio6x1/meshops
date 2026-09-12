import { computed, onUnmounted, ref } from 'vue'
import { api, calibrate, clockOffset, errorText, session } from './api'
import { applyUpdate, newStreamState } from './domain'
import type { EntityUpdate, Inventory } from './types'
// 每个页面持有一个 EventSource 和一个过期检查时钟，卸载时关闭两者。
export function useEntities() {
  const inventory = ref<Inventory[]>([]),
    state = ref(newStreamState()),
    connection = ref('尚未连接'),
    error = ref(''),
    loading = ref(false),
    now = ref(Date.now())
  let source: EventSource | undefined
  let disposed = false, revision = 0
  const controller = new AbortController()
  const timer = window.setInterval(() => {
    now.value = Date.now() + clockOffset.value
  }, 1000)
  const ready = computed(() => state.value.ready && ['已连接', '场景为空'].includes(connection.value))
  function connect() {
    if (disposed) return
    source?.close()
    state.value = newStreamState()
    if (!inventory.value.length) { state.value.ready = true; connection.value = '场景为空'; return }
    // 这里只订阅可信清单中的 ID；每页一个连接，切换页面时主动关闭。
    const ids = inventory.value
      .slice(0, 100)
      .map((v) => v.entityId)
      .join(',')
    source = new EventSource('/api/v1/entities/stream?' + new URLSearchParams({ entity_ids: ids }))
    connection.value = '正在同步'
    // TCP/SSE 建连成功不等于快照同步完成，必须继续等待 SNAPSHOT_END。
    source.onopen = () => {
      connection.value = '已连接'
      state.value.ready = false
    }
    source.addEventListener('update', (event) => {
      try {
        state.value = applyUpdate(
          state.value,
          JSON.parse((event as MessageEvent).data) as EntityUpdate,
        )
        error.value = ''
      } catch {
        error.value = '订阅数据无法解析，请重新同步'
        source?.close()
        connection.value = '已断开'
      }
    })
    source.addEventListener('error', (event) => {
      connection.value = '重连中'
      state.value.ready = false
      if (event instanceof MessageEvent) {
        try {
          error.value = JSON.parse(event.data).message
        } catch {
          error.value = '订阅中断'
        }
      }
      if (!session.value || Date.parse(session.value.expiresAt) <= Date.now() + clockOffset.value) {
        source?.close()
        session.value = null
      }
    })
  }
  async function load() {
    if (disposed) return
    const current = ++revision
    loading.value = true
    error.value = ''
    try {
      const data = await api<{ entities?: Inventory[]; serverTime?: string }>('/api/v1/entities', { signal: controller.signal })
      if (disposed || current !== revision) return
      inventory.value = data.entities ?? []
      calibrate(data.serverTime)
      connect()
    } catch (e) {
      if (!disposed && current === revision) error.value = errorText(e)
    } finally {
      if (!disposed && current === revision) loading.value = false
    }
  }
  onUnmounted(() => {
    disposed = true
    controller.abort()
    source?.close()
    clearInterval(timer)
  })
  return { inventory, state, connection, error, loading, now, ready, load, connect }
}
