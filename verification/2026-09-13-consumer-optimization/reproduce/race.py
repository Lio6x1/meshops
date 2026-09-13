from pathlib import Path
import subprocess,json,sys
root=Path(__file__).resolve().parent
results=[]
for package,pattern in [('bus','TestCommitBatch|TestKafkaUncommitted|TestPartitionFailure|TestStrictPerPartition'),('state','TestHistory|TestMySQLSample|TestRedisOrdering')]:
    if len(sys.argv)>1 and package!=sys.argv[1]:continue
    args=['docker','run','--rm','--name','meshops-consumer-opt-race-'+package,'--network','meshops-consumer-opt-tests_default','-e','MESHOPS_TEST_KAFKA_BROKERS=kafka:29092','-e','MESHOPS_TEST_MYSQL_ADMIN_DSN=root:course_local_root@tcp(mysql:3306)/meshops_course?parseTime=true&loc=UTC','-e','MESHOPS_TEST_REDIS_ADDR=redis:6379','-w','/src/internal/'+package,'meshops-consumer-opt:race','/out/'+package+'.test','-test.v','-test.count=1','-test.timeout=3m','-test.run',pattern]
    with (root/(package+'-race.txt')).open('w',encoding='utf-8') as output:
        result=subprocess.run(args,stdout=output,stderr=subprocess.STDOUT)
    results.append({'package':package,'exit':result.returncode,'pattern':pattern})
    print(package,'exit',result.returncode,flush=True)
(root/('race-results-'+(sys.argv[1] if len(sys.argv)>1 else 'all')+'.json')).write_text(json.dumps(results,indent=2)+'\n',encoding='utf-8')
if any(r['exit'] for r in results):raise SystemExit(1)
