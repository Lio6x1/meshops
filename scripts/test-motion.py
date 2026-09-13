"""Check actual mixed-scene movement via loopback HTTP; restore all settings."""
import importlib.util
import json
import math
import os
from pathlib import Path
import sys
import time

spec=importlib.util.spec_from_file_location('scene',Path(__file__).with_name('test-scene-counts.py'))
scene=importlib.util.module_from_spec(spec)
spec.loader.exec_module(scene)
sim=scene.sim

def main():
    output=Path(sys.argv[1])
    if output.exists():raise ValueError('Evidence output already exists')
    sim.login()
    original=scene.sources()
    report={'passed':False,'scope':'30 real HTTP snapshots; visible displacement of four moving types and fixed coordinates for sensor/facility; not a load benchmark.'}
    try:
        for source in original:
            scene.set_count(source['sourceId'],5)
            sim.api('/api/v1/simulation/'+source['sourceId'],'PUT',{'mode':'running'})
        sim.wait_for(lambda:scene.all_applied(5),20)
        # 完整轮询一遍需要 2.5 秒；记录基线前等待两轮，
        # 避免升级前缓存的位置进入本次位移测量。
        time.sleep(6)
        inventory=sim.api('/api/v1/entities')['entities']
        assert len(inventory)==30
        before={e['entityId']:sim.api('/api/v1/entities/'+e['entityId']) for e in inventory}
        time.sleep(5)
        checked=[]
        for entity in inventory:
            entity_id,kind=entity['entityId'],entity['entityType']
            first=before[entity_id]
            after=sim.api('/api/v1/entities/'+entity_id)
            assert first['found'] and after['found'] and first['version']!=after['version']
            a,b=first['snapshot']['location'],after['snapshot']['location']
            x=(b['longitude']-a['longitude'])*111320*math.cos(31.23*math.pi/180)
            y=(b['latitude']-a['latitude'])*111320
            metres=math.hypot(x,y)
            if kind in ('sensor','facility'):assert metres==0
            else:assert metres>=15,(entity_id,metres)
            checked.append({'entityId':entity_id,'entityType':kind,'displacementMetres':round(metres,2),'fromVersion':first['version'],'toVersion':after['version']})
        report['entities']=checked
        report['passed']=True
        print('PASS: 20 moving entities visibly changed position; 10 fixed entities stayed in place',flush=True)
    finally:
        restored=[]
        for source in original:
            try:
                scene.set_count(source['sourceId'],source['count'])
                sim.api('/api/v1/simulation/'+source['sourceId'],'PUT',{'mode':source['desired']})
                restored.append(source['sourceId'])
            except Exception:
                report['passed']=False
        report['restoredSources']=restored
        output.parent.mkdir(parents=True,exist_ok=True)
        output.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        sim.api('/api/session','DELETE')
        if len(restored)!=len(original):raise RuntimeError('Some source settings were not restored')

if __name__=='__main__':main()
