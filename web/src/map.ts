import type { EntityRecord, Inventory, Snapshot } from './types.ts'

// 以演示样例为原点，采用固定局部切平面近似。图幅为 1 km ×
// 640 m，仅作示意，不是测绘底图，也不是导航坐标接口。
// 固定坐标范围，避免单个实体移动时整幅地图跟着跳动。
export function projectLocation(location: Snapshot['location']): { x: number; y: number } | null {
  const lat = location?.latitude, lon = location?.longitude
  if (typeof lat !== 'number' || typeof lon !== 'number' || !Number.isFinite(lat) || !Number.isFinite(lon) || Math.abs(lat) > 90 || Math.abs(lon) > 180) return null
  const x = 500 + (lon - 121.47) * 111320 * Math.cos(31.23 * Math.PI / 180)
  const y = 320 - (lat - 31.23) * 111320
  return x >= 0 && x <= 1000 && y >= 0 && y <= 640 ? { x, y } : null
}
export function mapMarkers(inventory: Inventory[], records: Record<string, EntityRecord>, now: number) {
  return inventory.flatMap((entity) => {
    const record = records[entity.entityId]
    const point = projectLocation(record?.snapshot?.location)
    if (!point) return []
    const expires = Date.parse(record?.expiresAt ?? '')
    return [{ ...entity, ...point, stale: !Number.isFinite(expires) || expires <= now }]
  })
}
