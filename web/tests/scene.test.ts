import { test } from 'node:test'
import assert from 'node:assert/strict'
import { validSceneCount, sceneIDs } from '../src/scene.ts'

test('scene count supports zero through five and rejects fractions and missing values', () => {
  for (const value of [0,1,2,3,4,5]) assert.equal(validSceneCount(value),true)
  for (const value of [-1,6,1.5,undefined,null,NaN,Infinity,'5']) assert.equal(validSceneCount(value),false)
})
test('scene selection is deterministic and zero never means all entities', () => {
  const ids=['drone-003','drone-001','drone-002']
  assert.deepEqual(sceneIDs(ids,2),['drone-001','drone-002'])
  assert.deepEqual(sceneIDs(ids,0),[])
  assert.deepEqual(ids,['drone-003','drone-001','drone-002'])
})
