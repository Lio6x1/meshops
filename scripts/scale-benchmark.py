#!/usr/bin/env python3
"""标准库独立容量运行器；固定资源、总时限和脱敏证据。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import uuid

PROJECT = "meshops-scale"
SERVICES = ("mysql", "redis", "kafka", "runner")
# 严格字段白名单，禁止把完整 inspect（含环境凭证）落盘。
INSPECT_FORMAT = '{"id":{{json .Id}},"name":{{json .Name}},"status":{{json .State.Status}},"running":{{json .State.Running}},"oomKilled":{{json .State.OOMKilled}},"exitCode":{{json .State.ExitCode}},"error":{{json .State.Error}},"memoryLimit":{{json .HostConfig.Memory}},"nanoCpus":{{json .HostConfig.NanoCpus}},"health":{{with index .State "Health"}}{{json .Status}}{{else}}"none"{{end}}}'

def parse_args(argv=None):
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--entities",type=int,default=10000)
    parser.add_argument("--rates",default="100,500")
    parser.add_argument("--seconds",type=int,default=30)
    parser.add_argument("--deadline-seconds",type=int,default=900)
    parser.add_argument("--warmup-seconds",type=int,default=600)
    parser.add_argument("--sample-seconds",type=int,default=5)
    parser.add_argument("--image",default="meshops-demo-backend:local")
    args=parser.parse_args(argv)
    try: args.rates=[int(v.strip()) for v in args.rates.split(",")]
    except ValueError: parser.error("rates must be comma-separated integers")
    if not 10<=args.entities<=1000000 or args.entities%10: parser.error("entities must be 10..1000000 and divisible by 10")
    if not 1<=len(args.rates)<=10 or len(set(args.rates))!=len(args.rates) or any(not 1<=v<=100000 for v in args.rates):parser.error("rates must be 1..10 distinct integers in 1..100000")
    if not 5<=args.seconds<=300:parser.error("seconds must be 5..300")
    if not 10<=args.deadline_seconds<=10800:parser.error("deadline-seconds must be 10..10800")
    if not 1<=args.warmup_seconds<=7200:parser.error("warmup-seconds must be 1..7200")
    if not 1<=args.sample_seconds<=30:parser.error("sample-seconds must be 1..30")
    if any(c.isspace() for c in args.image) or "=" in args.image:parser.error("invalid image")
    return args

def compose_command(root,env_file):
    return ["docker","compose","--project-name",PROJECT,"--env-file",str(env_file),"-f",str(root/"compose.scale.yml")]

def prepare_secret(path):
    path.parent.mkdir(parents=True,exist_ok=True)
    try:
        fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    except FileExistsError: pass
    else:
        with os.fdopen(fd,"w",encoding="ascii") as stream:stream.write(secrets.token_hex(32)+"\n")
    raw=path.read_text(encoding="ascii").strip()
    if len(raw)!=64 or any(c not in "0123456789abcdef" for c in raw):raise ValueError("invalid local scale secret file")
    return hashlib.sha256(raw.encode()).hexdigest()

def redact(text,secret):return text.replace(secret,"[REDACTED]") if secret else text

def classify(timed_out,states,exit_code,report):
    if any(s.get("oomKilled") or s.get("cgroupOomKillCount",0)>0 for s in states):return "oom"
    if timed_out:return "timeout"
    if report and "under_offered" in report.get("failure",""):return "under_offered"
    if exit_code not in (0,None) or (report and report.get("failure")):return "failed"
    if not report:return "missing_report"
    if report.get("stage")!="complete":return "incomplete"
    return "passed"

class Runner:
    def __init__(self,args,root):
        self.args=args;self.root=root
        self.local=root/".local"/"scale"
        self.output=self.local/"runs"/(time.strftime("%Y%m%dT%H%M%S")+"-"+uuid.uuid4().hex[:8])
        self.output.mkdir(parents=True,mode=0o700)
        self.env_file=self.output/"compose.env"
        self.base=compose_command(root,self.env_file)
        self.end=time.monotonic()+args.deadline_seconds
        self.secret="";self.states=[];self.ids={};self.started=False
        # 固定调优参数属于实验条件；redo 是磁盘日志容量，不增加容器内存额度。
        self.mysql_tuning={"bufferPoolBytes":256*1024**2,"redoCapacityBytes":1024**3}
        self.result={"project":PROJECT,"requested":vars(args),"stage":"prepare","outcome":"incomplete","startedAt":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime()),"evidenceDirectory":str(self.output),"resources":{"runner":{"cpus":4,"bytes":3*1024**3},"mysql":{"cpus":2,"bytes":1024**3},"kafka":{"cpus":2,"bytes":1536*1024**2},"redis":{"cpus":2,"bytes":1536*1024**2,"maxmemory":1024**3}}}

    def command(self,command,timeout=15,cleanup=False,check=True):
        budget=timeout if cleanup else min(timeout,max(0.01,self.end-time.monotonic()))
        done=subprocess.run(command,cwd=self.root,capture_output=True,text=True,encoding="utf-8",errors="replace",timeout=budget,creationflags=getattr(subprocess,"CREATE_NO_WINDOW",0))
        if check and done.returncode:raise RuntimeError("command failed: "+redact(done.stderr[-4000:],self.secret))
        return done

    def save(self):
        self.result["configuredMySQLTuning"]=self.mysql_tuning
        (self.output/"summary.json").write_text(redact(json.dumps(self.result,ensure_ascii=False,indent=2),self.secret),encoding="utf-8")

    def discover(self,include_runner=True):
        for service in (SERVICES if include_runner else SERVICES[:-1]):
            found=self.command(self.base+["ps","--all","--quiet",service]).stdout.strip().splitlines()
            if found:self.ids[service]=found[0]

    def inspect(self,cleanup=False):
        if not self.ids:return []
        raw=self.command(["docker","inspect","--format",INSPECT_FORMAT,*self.ids.values()],cleanup=cleanup).stdout
        states=[json.loads(line) for line in raw.splitlines() if line.strip()]
        self.states=states
        return states

    def sample(self):
        states=self.inspect()
        # 子进程被 cgroup OOM 杀死时，Docker 顶层 OOMKilled 可能仍为 false。
        runner=next((s for s in states if s["id"]==self.ids.get("runner") and s["running"]),None)
        if runner:
            event=self.command(["docker","exec",self.ids["runner"],"cat","/sys/fs/cgroup/memory.events"],timeout=5,check=False)
            counts={}
            for line in event.stdout.splitlines():
                parts=line.split()
                if len(parts)==2 and parts[1].isdigit():counts[parts[0]]=int(parts[1])
            # 读取失败或不支持该指标时证据不完整，不能把缺失值冒充零次 OOM。
            if event.returncode!=0 or "oom_kill" not in counts:
                self.result["resourceSampleErrors"]=self.result.get("resourceSampleErrors",0)+1
            runner["cgroupMemoryEvents"]=counts
            runner["cgroupOomKillCount"]=counts.get("oom_kill",0)
            self.result["cgroupOomKillCount"]=max(self.result.get("cgroupOomKillCount",0),runner["cgroupOomKillCount"])
        stats=self.command(["docker","stats","--no-stream","--format","{{json .}}",*self.ids.values()],timeout=10,check=False)
        if stats.returncode!=0:self.result["resourceSampleErrors"]=self.result.get("resourceSampleErrors",0)+1
        record={"at":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime()),"states":states,"stats":[json.loads(line) for line in stats.stdout.splitlines() if line.strip()],"statsExit":stats.returncode}
        with (self.output/"resources.jsonl").open("a",encoding="utf-8") as stream:stream.write(redact(json.dumps(record),self.secret)+"\n")
        return states

    def wait_dependencies(self):
        self.discover(include_runner=False)
        while True:
            if time.monotonic()>=self.end:raise TimeoutError("hard deadline during dependency startup")
            states=self.sample()
            if any(s["oomKilled"] or (not s["running"] and s["exitCode"]!=0) for s in states):raise RuntimeError("dependency exited during startup")
            if len(states)==3 and all(s["health"]=="healthy" for s in states):return
            time.sleep(min(self.args.sample_seconds,max(0,self.end-time.monotonic())))

    def execute(self):
        lock=self.local/"run.lock"
        lock_fd=None;timed_out=False;exit_code=None;report=None
        try:
            lock_fd=os.open(lock,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
            os.write(lock_fd,str(os.getpid()).encode())
            secret_file=self.local/"mysql-root-password"
            self.result["secretSHA256"]=prepare_secret(secret_file)
            self.secret=secret_file.read_text(encoding="ascii").strip()
            env={"SCALE_SECRET_FILE":secret_file.as_posix(),"SCALE_OUTPUT_DIR":self.output.as_posix(),"SCALE_IMAGE":self.args.image,"SCALE_ENTITIES":str(self.args.entities),"SCALE_RATES":",".join(map(str,self.args.rates)),"SCALE_SECONDS":str(self.args.seconds),"SCALE_TIMEOUT":str(self.args.deadline_seconds),"SCALE_WARMUP":str(self.args.warmup_seconds)}
            self.env_file.write_text("".join(f'{k}={v}\n' for k,v in env.items()),encoding="utf-8")
            self.command(self.base+["config","--quiet"])
            self.result["docker"]=json.loads(self.command(["docker","version","--format","{{json .Server}}"] ).stdout)
            # 仅该固定项目；从未对演示项目或无关容器执行操作。
            self.started=True;self.result["stage"]="dependencies";self.save()
            self.command(self.base+["up","-d","mysql","redis","kafka"],timeout=60)
            self.wait_dependencies()
            self.result["stage"]="benchmark";self.save()
            self.command(self.base+["up","-d","--no-deps","--force-recreate","runner"],timeout=30)
            self.discover()
            while True:
                if time.monotonic()>=self.end:raise TimeoutError("hard benchmark deadline")
                states=self.sample()
                if any(s["oomKilled"] or s.get("cgroupOomKillCount",0)>0 for s in states):raise RuntimeError("container OOM")
                running=next((s for s in states if s["id"]==self.ids.get("runner")),None)
                if running and not running["running"]:exit_code=running["exitCode"];break
                if any(not s["running"] for s in states):raise RuntimeError("dependency exited during benchmark")
                print(f'scale running; evidence: {self.output}',file=sys.stderr,flush=True)
                time.sleep(min(self.args.sample_seconds,max(0,self.end-time.monotonic())))
        except (subprocess.TimeoutExpired,TimeoutError) as error:
            timed_out=True;self.result["error"]=type(error).__name__+": hard deadline reached"
        except Exception as error:
            self.result["error"]=redact(str(error),self.secret)
        finally:
            if self.started:
                # 外部硬时限到达后先停压；清理本身也有独立、有限的命令时限。
                if timed_out:
                    try:self.command(self.base+["kill","runner"],timeout=10,cleanup=True,check=False)
                    except Exception as error:self.result["killError"]=str(error)
                try:
                    self.inspect(cleanup=True)
                except Exception as error:self.result["inspectError"]=str(error)
                try:
                    logs=self.command(self.base+["logs","--no-color","--timestamps","--tail","20000"],timeout=15,cleanup=True,check=False)
                    (self.output/"containers.log").write_text(redact(logs.stdout+logs.stderr,self.secret),encoding="utf-8")
                except Exception as error:self.result["evidenceError"]=str(error)
                try:
                    stop=self.command(self.base+["stop","--timeout","10"],timeout=30,cleanup=True,check=False)
                    self.result["stopExitCode"]=stop.returncode
                except Exception as error:self.result["stopError"]=str(error)
            files=list((self.output/".local"/"verification").glob("*/benchmark.json"))
            if len(files)==1:
                try:report=json.loads(files[0].read_text(encoding="utf-8"));self.result["benchmark"]=report
                except (ValueError,OSError) as error:self.result["reportError"]=str(error)
            self.result.update({"states":self.states,"exitCode":exit_code,"timedOut":timed_out,"outcome":classify(timed_out,self.states,exit_code,report),"finishedAt":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime())})
            if self.result.get("cgroupOomKillCount",0)>0:self.result["outcome"]="oom"
            if self.result["outcome"] in ("missing_report","incomplete") and "error" in self.result:self.result["outcome"]="failed"
            if self.result["outcome"]=="passed" and any(k in self.result for k in ("error","stopError","evidenceError","inspectError","reportError","resourceSampleErrors")):self.result["outcome"]="failed"
            if self.result["outcome"]=="passed" and self.result.get("stopExitCode",0)!=0:self.result["outcome"]="failed"
            if self.result["outcome"]=="passed" and self.result.get("resourceSampleErrors",0):self.result["outcome"]="failed"
            self.save()
            if lock_fd is not None:os.close(lock_fd);lock.unlink()
        print(json.dumps({"outcome":self.result["outcome"],"summary":str(self.output/"summary.json")},ensure_ascii=False))
        return 0 if self.result["outcome"]=="passed" else 1

def main(argv=None):return Runner(parse_args(argv),Path(__file__).resolve().parent.parent).execute()
if __name__=="__main__":sys.exit(main())
