from pathlib import Path
import importlib.util,json,subprocess,time
HERE=Path(__file__).resolve().parent; ROOT=HERE.parents[1]; SOURCE=HERE/'source'
spec=importlib.util.spec_from_file_location('scale',ROOT/'scripts/scale-benchmark.py');scale=importlib.util.module_from_spec(spec);spec.loader.exec_module(scale)
scale.PROJECT='meshops-consumer-compare'
results=[]
for trial,instances in enumerate((1,),5):
 override=SOURCE/'compare-override.yml';override.write_text('services:\n  runner:\n    environment:\n      MESHOPS_COMPARE_INSTANCES: "'+str(instances)+'"\n',encoding='utf-8')
 args=scale.parse_args(['--entities','10000','--rates','2000,5000','--seconds','30','--deadline-seconds','720','--warmup-seconds','300','--sample-seconds','5','--image','meshops-consumer-compare:experiment'])
 runner=scale.Runner(args,SOURCE);runner.base+=['-f',str(override)]
 runner.result['comparison']={'trial':trial,'instances':instances,'order':[1,3,3,1],'initialVolumes':'fresh','demo':'left running; not modified','historyPolicy':'unchanged: 100 eligible entities, budget 200 per instance and periodic 100; record actual writes and skip counters'}
 print('TRIAL_START',trial,instances,flush=True)
 code=runner.execute();data=json.loads((runner.output/'summary.json').read_text(encoding='utf-8'))
 results.append({'trial':trial,'instances':instances,'outcome':data['outcome'],'exit':code,'path':str(runner.output.relative_to(HERE))})
 (HERE/'extra-trials.json').write_text(json.dumps(results,indent=2)+'\n',encoding='utf-8')
 # 每一轮用同样的空数据卷；只删除本试验固定项目拥有的数据。
 volumes=runner.command(['docker','volume','ls','-q','--filter','label=com.docker.compose.project='+scale.PROJECT],cleanup=True).stdout.splitlines()
 for v in volumes:
  obj=json.loads(runner.command(['docker','volume','inspect',v],cleanup=True).stdout)[0]
  if obj.get('Labels',{}).get('com.docker.compose.project')!=scale.PROJECT or not v.startswith(scale.PROJECT+'_'):raise RuntimeError('unexpected volume ownership')
 runner.command(runner.base+['down','--volumes'],timeout=60,cleanup=True)
 print('TRIAL_END',trial,instances,data['outcome'],flush=True)
print('COMPARISON_DONE',flush=True)
