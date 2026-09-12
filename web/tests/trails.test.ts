import {test} from 'node:test'
import assert from 'node:assert/strict'
import {advanceTrails} from '../src/trails.ts'
const inventory=[{entityId:'drone-001',entityType:'drone'}]
const record=(version:string,generation='1')=>({'drone-001':{version,sourceId:'source',sourceGeneration:generation,viewGeneration:'view',expiresAt:new Date(90000).toISOString(),snapshot:{location:{latitude:31.23,longitude:121.47+Number(version)*.000001}}}})
test('trails are bounded and repeated versions do not invent movement',()=>{
 let trails=advanceTrails({},inventory,record('1'),1000,true)
 trails=advanceTrails(trails,inventory,record('1'),1500,true)
 assert.equal(trails['drone-001']!.points.length,1)
 for(let i=2;i<=40;i++)trails=advanceTrails(trails,inventory,record(String(i)),i*500,true)
 assert.equal(trails['drone-001']!.points.length,20)
 trails=advanceTrails(trails,inventory,record('40'),60000,true)
 assert.equal(trails['drone-001']!.points.length,0)
})
test('resync, inactive inventory, invalid coordinates and source changes break trails',()=>{
 const trails=advanceTrails({},inventory,record('1'),1000,true)
 assert.deepEqual(advanceTrails(trails,inventory,record('2'),1500,false),{})
 assert.deepEqual(advanceTrails(trails,[],record('2'),1500,true),{})
 assert.deepEqual(advanceTrails(trails,inventory,{'drone-001':{version:'2'}},1500,true),{})
 assert.equal(advanceTrails(trails,inventory,record('2','2'),1500,true)['drone-001']!.points.length,1)
 assert.deepEqual(advanceTrails({},[{entityId:'drone-001',entityType:'sensor'}],record('1'),1000,true),{})
})
