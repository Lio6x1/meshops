export function validSceneCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 5
}
export function sceneIDs(ids: string[], count: number): string[] {
  return validSceneCount(count) ? [...ids].sort().slice(0, count) : []
}
