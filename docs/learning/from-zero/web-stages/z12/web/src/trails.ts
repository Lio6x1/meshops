import type {EntityRecord, Inventory} from './types.ts'
import {projectLocation} from './map.ts'
export interface Trail { identity:string; version:string; points:{x:number;y:number;at:number}[] }
export type Trails=Record<string,Trail>

// Page-local evidence of received positions, never predicted motion. Both a
// point cap and an age cap bound memory, even during long-running demos.
export function advanceTrails(previous:Trails,inventory:Inventory[],records:Record<string,EntityRecord>,now:number,ready:boolean):Trails {
 if(!ready)return {}
 const next:Trails={}
 for(const entity of inventory){
  if(!['person','drone','vehicle','robot'].includes(entity.entityType))continue
  const record=records[entity.entityId],point=projectLocation(record?.snapshot?.location)
  if(!point || !record?.version)continue
  const identity=JSON.stringify([record.sourceId,record.sourceGeneration,record.viewGeneration])
  const old=previous[entity.entityId]
  let points=old?.identity===identity?old.points.filter(p=>now-p.at<=30000):[]
  if(old?.identity!==identity || old.version!==record.version){
   // Avoid a straight segment across a pause/disconnection or stale replay.
   if(points.length && now-points[points.length-1]!.at>6000)points=[]
   const expires=Date.parse(record.expiresAt??'')
   if(Number.isFinite(expires)&&expires>now)points=[...points,{...point,at:now}].slice(-20)
  }
  next[entity.entityId]={identity,version:record.version,points}
 }
 return next
}
