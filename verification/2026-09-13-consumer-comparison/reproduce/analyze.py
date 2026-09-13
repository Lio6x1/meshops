from pathlib import Path
import json,re,hashlib
root=Path(__file__).resolve().parent

def counters(sample):
 out={}
 for role,body in sample.get('serviceMetrics',{}).items():
  for line in body.splitlines():
   m=re.match(r'^(meshops_compare_work_seconds_(?:sum|count)|meshops_compare_history_lock_seconds_(?:sum|count)|meshops_history_candidates_total|process_cpu_seconds_total|go_memstats_heap_alloc_bytes)(?:\{([^}]*)\})?\s+([0-9.eE+\-]+)',line)
   if not m:continue
   name,labels,value=m.groups();labels=dict(re.findall(r'(\w+)="([^"]*)"',labels or ''))
   group='history' if 'history' in labels.get('group','') else 'projection'
   key=(name,labels.get('phase',labels.get('reason','')),group if 'work_seconds' in name else '')
   out[key]=out.get(key,0)+float(value)
 return out
rows=[]
for run in sorted((root/'source/.local/scale/runs').glob('*')):
 try:s=json.loads((run/'summary.json').read_text(encoding='utf-8'))
 except (OSError,ValueError):continue
 if s.get('outcome')=='incomplete':continue
 b=s.get('benchmark',{});entry={'run':run.name,'instances':s.get('comparison',{}).get('instances'),'trial':s.get('comparison',{}).get('trial'),'outcome':s.get('outcome'),'failure':b.get('failure',s.get('error')),'stage':b.get('stage'),'warmupAccepted':b.get('WarmupAccepted'),'warmupSnapshots':b.get('WarmupSnapshots'),'phases':b.get('Phases'),'timing':[]}
 for file in (run/'.local/verification').glob('*/phase-diagnostics.jsonl'):
  for line in file.read_text(encoding='utf-8').splitlines():
   p=json.loads(line);a=counters(p['before']);z=counters(p['after']);delta={key:z.get(key,0)-a.get(key,0) for key in a.keys()|z.keys()};timing={'rate':p['rate'],'warmup':p['warmup'],'historyRows':p['historyRows'],'historyCountOK':p['historyCountOK'],'groups':{},'historyCandidates':{},'errors':p['before'].get('errors',[])+p['after'].get('errors',[])}
   for group in ('projection','history'):
    measured={}
    for phase in ('handler','commit'):
     seconds=delta.get(('meshops_compare_work_seconds_sum',phase,group),0);count=delta.get(('meshops_compare_work_seconds_count',phase,group),0);measured[phase]={'seconds':seconds,'calls':count,'meanMs':1000*seconds/count if count else None}
    both=measured['handler']['seconds']+measured['commit']['seconds'];measured['commitShareOfMeasuredWork']=measured['commit']['seconds']/both if both>0 else None;timing['groups'][group]=measured
   for phase in ('wait','hold'):
    total=delta.get(('meshops_compare_history_lock_seconds_sum',phase,''),0);count=delta.get(('meshops_compare_history_lock_seconds_count',phase,''),0);timing['lock_'+phase]={'seconds':total,'calls':count,'meanMs':1000*total/count if count else None}
   for key,value in delta.items():
    if key[0]=='meshops_history_candidates_total':timing['historyCandidates'][key[1]]=value
   entry['timing'].append(timing)
 rows.append(entry)
(root/'analysis.json').write_text(json.dumps(rows,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
for r in rows:
 print('trial',r['trial'],'instances',r['instances'],r['outcome'],r['failure'])
 for p in r['phases'] or []:print('  rate',p['offeredEventsPerSecond'],'actual',round(p['acceptedEventsPerSecond'],1),'drops',p['generatorQueueDrops'],'visibleP99ms',round(p['generatedToObserved']['p99Ms'],2),'lag',p['projectorLag'],p['historyLag'])
 for t in r['timing']:
  if not t['warmup']:print('  work rate',t['rate'],'commit share',{g:round(v['commitShareOfMeasuredWork'] or 0,3) for g,v in t['groups'].items()},'lockwait/hold',round(t['lock_wait']['seconds'],3),round(t['lock_hold']['seconds'],3),'history outcomes',t['historyCandidates'])
