from pathlib import Path
import subprocess
r=Path.cwd();dest=r/'.cache/consumer-compare/source';dest.mkdir(exist_ok=True)
files=subprocess.check_output(['git','ls-files','-z']).decode().split('\0')
for name in files:
 if name and (name.startswith(('cmd/','internal/','gen/','configs/','migrations/','testdata/','deploy/','proto/')) or name in ('go.mod','go.sum','Dockerfile','.dockerignore','compose.scale.yml','docker-compose.yml','compose.search.yml','compose.demo.yml','README.md')):
  p=dest/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes((r/name).read_bytes())
def patch(name,a,b):
 p=dest/name;s=p.read_text(encoding='utf-8');assert s.count(a)==1,(name,a,s.count(a));p.write_text(s.replace(a,b),encoding='utf-8',newline='\n')
patch('internal/verification/environment.go','name := role\n', 'name := role\n\tif strings.HasPrefix(role, "entity-") { name = "entity" }\n')
patch('internal/verification/benchmark.go','type BenchmarkReport struct {','type BenchmarkReport struct {\n\tConsumerInstances int `json:"consumerInstances"`')
patch('internal/verification/benchmark.go','id := strings.ReplaceAll(platform.NewID(), "-", "")','instances := 1\n\tif raw := os.Getenv("MESHOPS_COMPARE_INSTANCES"); raw != "" { instances, err = strconv.Atoi(raw); if err != nil || (instances != 1 && instances != 3) { return report, fmt.Errorf("comparison requires 1 or 3 instances") } }\n\tid := strings.ReplaceAll(platform.NewID(), "-", "")')
patch('internal/verification/benchmark.go','if err = os.MkdirAll(report.EvidenceDirectory, 0700); err != nil {','report.ConsumerInstances = instances\n\tif err = os.MkdirAll(report.EvidenceDirectory, 0700); err != nil {')
patch('internal/verification/benchmark.go','for _, role := range []string{"entity", "ingest"} {','roles := []string{"entity"}\n\tfor i := 2; i <= instances; i++ { role := fmt.Sprintf("entity-%d", i); a, x := address(); if x != nil { return report, x }; m, x := address(); if x != nil { a.Close(); return report, x }; env.Processes[role] = &Process{Endpoint:a.Addr().String(), Metrics:m.Addr().String(), reservations:[]net.Listener{a,m}}; roles=append(roles, role) }\n\troles=append(roles,"ingest")\n\tfor _, role := range roles {')
patch('internal/verification/benchmark.go','"net/http"','"net/http"\n\t"net"')
patch('internal/verification/benchmark.go','stopDiagnostics, err := startDiagnostics(ctx, env)','if err = env.waitComparisonAssignment(ctx, instances); err != nil { return report, err }\n\tstopDiagnostics, err := startDiagnostics(ctx, env)')
patch('internal/verification/benchmark.go','start := time.Now()\n\tresult := Phase{','before := captureDiagnostics(ctx,e)\n\tdefer func(){ comparisonSaveDiagnostics(e, rate, warmup, before) }()\n\tstart := time.Now()\n\tresult := Phase{')
patch('internal/verification/diagnostics.go','"path/filepath"','"path/filepath"\n\t"sort"\n\t"strings"')
patch('internal/verification/diagnostics.go','for _, role := range []string{"ingest", "entity"} {','roles:=[]string{}\n\tfor role := range env.Processes { if (role=="ingest" || strings.HasPrefix(role,"entity")) { roles=append(roles,role) } };sort.Strings(roles)\n\tfor _, role := range roles {')
patch('internal/bus/kafka.go','"time"','"time"\n\t"github.com/prometheus/client_golang/prometheus"\n\t"github.com/prometheus/client_golang/prometheus/promauto"\n\t"strconv"')
patch('internal/bus/kafka.go','const operationTimeout = 5 * time.Second','var comparisonWork = promauto.NewSummaryVec(prometheus.SummaryOpts{Name:"meshops_compare_work_seconds",Help:"Temporary comparison wall time; no quantile computation."},[]string{"phase","group","partition"})\nconst operationTimeout = 5 * time.Second')
patch('internal/bus/kafka.go','expected := partition.Offset','handlerTime := comparisonWork.WithLabelValues("handler",group,strconv.Itoa(partition.ID))\n\tcommitTime := comparisonWork.WithLabelValues("commit",group,strconv.Itoa(partition.ID))\n\texpected := partition.Offset')
patch('internal/bus/kafka.go','if err = handler(recordCtx, m.Value); err == nil {','began := time.Now();err = handler(recordCtx,m.Value);handlerTime.Observe(time.Since(began).Seconds())\n\t\t\tif err == nil {')
patch('internal/bus/kafka.go','err = generation.CommitOffsets(map[string]map[int]int64{topic: {m.Partition: m.Offset + 1}})','began:=time.Now()\n\t\t\terr = generation.CommitOffsets(map[string]map[int]int64{topic: {m.Partition: m.Offset + 1}})\n\t\t\tcommitTime.Observe(time.Since(began).Seconds())')
patch('internal/state/metrics.go','var projectionResults =','var comparisonLock = promauto.NewSummaryVec(prometheus.SummaryOpts{Name:"meshops_compare_history_lock_seconds",Help:"Temporary history lock comparison."},[]string{"phase"})\nvar projectionResults =')
patch('internal/state/history.go','e.historyMu.Lock()\n\tdefer e.historyMu.Unlock()','waitStart:=time.Now()\n\te.historyMu.Lock()\n\tcomparisonLock.WithLabelValues("wait").Observe(time.Since(waitStart).Seconds())\n\tholdStart:=time.Now()\n\tdefer func(){ comparisonLock.WithLabelValues("hold").Observe(time.Since(holdStart).Seconds());e.historyMu.Unlock() }()')
helper='''package verification
import("context";"encoding/json";"fmt";"os";"path/filepath";"strings";"time";"github.com/segmentio/kafka-go")
// 临时实验：确认两个消费组成员数及三个分区恰好各分配一次，再开始供压。
func(e *Environment) waitComparisonAssignment(ctx context.Context,n int)error{
 ctx,cancel:=context.WithTimeout(ctx,90*time.Second);defer cancel()
 client:=&kafka.Client{Addr:kafka.TCP(strings.Split(e.env["MESHOPS_KAFKA_BROKERS"],",")...),Timeout:5*time.Second}
 for ctx.Err()==nil {
  response,err:=client.DescribeGroups(ctx,&kafka.DescribeGroupsRequest{GroupIDs:[]string{e.projectorGroup,e.Prefix+"entity-history-v1"}})
  valid:=err==nil && response!=nil && len(response.Groups)==2
  if valid {for _,g:=range response.Groups{if g.Error!=nil || g.GroupState!="Stable" || len(g.Members)!=n{valid=false};seen:=map[int]int{};for _,m:=range g.Members{for _,topic:=range m.MemberAssignments.Topics{if topic.Topic!=e.Prefix+"entity-state-events.v1"{valid=false};for _,p:=range topic.Partitions{seen[p]++}}};if len(seen)!=3||seen[0]!=1||seen[1]!=1||seen[2]!=1{valid=false}}}
  if valid {raw,err:=json.MarshalIndent(response,"","  ");if err!=nil{return err};return os.WriteFile(filepath.Join(e.Dir,"assignments.json"),raw,0600)}
  select{case<-ctx.Done():case<-time.After(time.Second):}
 };return fmt.Errorf("comparison groups not stable with %d members: %w",n,ctx.Err())
}
func comparisonSaveDiagnostics(e *Environment,rate int,warm bool,before diagnosticSample){
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();after:=captureDiagnostics(ctx,e)
 var count int64;err:=e.DB.QueryRowContext(ctx,"SELECT COUNT(*) FROM entity_history_samples").Scan(&count)
 f,x:=os.OpenFile(filepath.Join(e.Dir,"phase-diagnostics.jsonl"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if x!=nil{panic(x)};defer f.Close()
 record:=map[string]any{"rate":rate,"warmup":warm,"before":before,"after":after,"historyRows":count,"historyCountOK":err==nil};if err=json.NewEncoder(f).Encode(record);err!=nil{panic(err)}
}
'''
(dest/'internal/verification/comparison.go').write_text(helper,encoding='utf-8',newline='\n')
(dest/'deploy/scale/run.sh').write_text((dest/'deploy/scale/run.sh').read_text(encoding='utf-8').replace('exec /app/verify','export MESHOPS_COMPARE_INSTANCES\nexec /app/verify'),encoding='utf-8',newline='\n')
print('Prepared temporary experiment source:',dest)

p=dest/'Dockerfile';s=p.read_text(encoding='utf-8');s=s.replace('COPY migrations ./migrations\nRUN --mount', 'COPY migrations ./migrations\nCOPY testdata ./testdata\nCOPY configs ./configs\nCOPY compose.demo.yml ./\nRUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go test ./internal/verification ./internal/bus\nRUN --mount',1);p.write_text(s,encoding='utf-8',newline='\n')

p=dest/"compose.scale.yml";s=p.read_text(encoding="utf-8").replace("mysqladmin ping -h localhost --silent","mysqladmin ping -h 127.0.0.1 --silent");p.write_text(s,encoding="utf-8",newline="\n")
