import { test } from 'node:test'
import assert from 'node:assert/strict'
import { projectLocation, mapMarkers } from '../src/map.ts'

test('map uses backend coordinates with east right and north up', () => {
  const center = projectLocation({ latitude: 31.23, longitude: 121.47 })!
  assert.equal(center.x, 500)
  assert.equal(center.y, 320)
  assert.ok(projectLocation({ latitude: 31.2301, longitude: 121.4701 })!.x > center.x)
  assert.ok(projectLocation({ latitude: 31.2301, longitude: 121.4701 })!.y < center.y)
})
test('map never invents a location or clamps out-of-site entities onto the map', () => {
  for (const location of [undefined, {}, { latitude: NaN, longitude: 121.47 }, { latitude: 91, longitude: 0 }, { latitude: 0, longitude: 0 }]) {
    assert.equal(projectLocation(location), null)
  }
})
test('map respects inventory and preserves stale last positions', () => {
  const markers = mapMarkers([{ entityId: 'drone-001', entityType: 'drone' }], {
    'drone-001': { snapshot: { location: { latitude: 31.23, longitude: 121.47 } }, expiresAt: '2026-01-01T00:00:00Z' },
    secret: { snapshot: { location: { latitude: 31.23, longitude: 121.47 } } },
  }, Date.parse('2026-01-02T00:00:00Z'))
  assert.equal(markers.length, 1)
  assert.equal(markers[0]!.entityId, 'drone-001')
  assert.equal(markers[0]!.stale, true)
})
