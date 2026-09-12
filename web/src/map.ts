import type { EntityRecord, Inventory, Snapshot } from './types.ts'

// Fixed local tangent-plane approximation around the demo fixtures. The 1 km ×
// 640 m drawing is schematic, not a surveyed basemap or a navigation coordinate API.
// A fixed frame prevents the whole map from jumping when an entity moves.
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
