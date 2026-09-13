from pathlib import Path
import json,re
root=Path(__file__).resolve().parent
rows=[]
for variant in ('baseline','batch','both'):
    for run in (root/variant/'.local/scale/runs').glob('*'):
        try:s=json.loads((run/'summary.json').read_text(encoding='utf-8'))
        except (OSError,ValueError):continue
        if s.get('outcome')=='incomplete':continue
        b=s.get('benchmark',{});phases=b.get('Phases') or []
        row={'run':run.name,**s['comparison'],'outcome':s['outcome'],'failure':b.get('failure') or s.get('error') or ('resource sampling incomplete' if s.get('resourceSampleErrors') else None),'warmupSnapshots':b.get('WarmupSnapshots'),'phases':phases,'sampledHistoryCounterMax':{},'resourcePeaks':{}}
        for file in (run/'.local/verification').glob('*/diagnostics.jsonl'):
            for line in file.read_text(encoding='utf-8').splitlines():
                sample=json.loads(line)
                for body in sample.get('serviceMetrics',{}).values():
                    for reason,value in re.findall(r'^meshops_history_candidates_total\{reason="([^"]+)"\} ([0-9.eE+\-]+)',body,re.M):
                        row['sampledHistoryCounterMax'][reason]=max(row['sampledHistoryCounterMax'].get(reason,0),float(value))
        for line in (run/'resources.jsonl').read_text(encoding='utf-8').splitlines():
            for stat in json.loads(line).get('stats',[]):
                role=stat['Name'].removeprefix('meshops-consumer-opt-');peak=row['resourcePeaks'].setdefault(role,{'cpuPercent':0,'memoryPercent':0})
                peak['cpuPercent']=max(peak['cpuPercent'],float(stat['CPUPerc'].rstrip('%')))
                peak['memoryPercent']=max(peak['memoryPercent'],float(stat['MemPerc'].rstrip('%')))
        rows.append(row)
rows.sort(key=lambda r:r['trial'])
(root/'analysis.json').write_text(json.dumps(rows,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
for r in rows:
    print(r['trial'],r['variant'],r['outcome'],r['failure'])
    for p in r['phases']:print(' throughput',round(p['acceptedEventsPerSecond'],1),'drops',p['generatorQueueDrops'],'visibleP99',p['generatedToObserved']['p99Ms'],'lag',p['projectorLag'],p['historyLag'])
