import type {EntityRecord, Inventory} from './types.ts'
import {projectLocation} from './map.ts'
export interface Trail { identity:string; version:string; points:{x:number;y:number;at:number}[] }
export type Trails=Record<string,Trail>

// 仅记录本页实际收到的位置，不预测运动。轨迹点数和保留时长
// 都有上限，即使演示长期运行也不会持续增加内存占用。
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
   // 避免暂停、断线或过期数据重放后画出跨越断档的直线。
   if(points.length && now-points[points.length-1]!.at>6000)points=[]
   const expires=Date.parse(record.expiresAt??'')
   if(Number.isFinite(expires)&&expires>now)points=[...points,{...point,at:now}].slice(-20)
  }
  next[entity.entityId]={identity,version:record.version,points}
 }
 return next
}
