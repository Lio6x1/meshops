from pathlib import Path
import importlib.util,json
here=Path(__file__).resolve().parent;repo=here.parents[1]
spec=importlib.util.spec_from_file_location('scale',repo/'scripts/scale-benchmark.py');scale=importlib.util.module_from_spec(spec);spec.loader.exec_module(scale)
scale.PROJECT='meshops-consumer-opt'
results=[]
for trial,variant in enumerate(('baseline','batch','both','both','batch','baseline'),1):
    args=scale.parse_args(['--entities','10000','--rates','2000','--seconds','30','--deadline-seconds','720','--warmup-seconds','300','--sample-seconds','5','--image','meshops-consumer-opt:'+variant])
    runner=scale.Runner(args,here/variant)
    runner.result['comparison']={'trial':trial,'variant':variant,'instances':1,'initialVolumes':'fresh','demo':'left running','instrumentation':'none; same standard verifier'}
    print('TRIAL_START',trial,variant,flush=True)
    code=runner.execute();s=json.loads((runner.output/'summary.json').read_text(encoding='utf-8'))
    results.append({'trial':trial,'variant':variant,'outcome':s['outcome'],'exit':code,'path':str(runner.output.relative_to(here))})
    (here/'trials.json').write_text(json.dumps(results,indent=2)+'\n',encoding='utf-8')
    volumes=runner.command(['docker','volume','ls','-q','--filter','label=com.docker.compose.project='+scale.PROJECT],cleanup=True).stdout.splitlines()
    for volume in volumes:
        item=json.loads(runner.command(['docker','volume','inspect',volume],cleanup=True).stdout)[0]
        assert item.get('Labels',{}).get('com.docker.compose.project')==scale.PROJECT and volume.startswith(scale.PROJECT+'_')
    runner.command(runner.base+['down','--volumes'],timeout=60,cleanup=True)
    phase=(s.get('benchmark',{}).get('Phases') or [{}])[0]
    print('TRIAL_END',trial,variant,s['outcome'],phase.get('acceptedEventsPerSecond'),flush=True)
print('COMPARISON_DONE',flush=True)
