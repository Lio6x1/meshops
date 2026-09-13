"""不启动 Docker 的容量入口边界测试。"""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock
import subprocess

SPEC=importlib.util.spec_from_file_location("scale",Path(__file__).with_name("scale-benchmark.py"))
scale=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(scale)

class ScaleTests(unittest.TestCase):
    def test_defaults_and_independent_dimensions(self):
        args=scale.parse_args([])
        self.assertEqual((args.entities,args.rates,args.seconds),(10000,[100,500],30))
        args=scale.parse_args(["--entities","1000000","--rates","500,16667"])
        self.assertEqual(args.rates,[500,16667])
        for argv in [["--entities","1000010"],["--entities","11"],["--rates","0"],["--rates","1,1"],["--rates","100001"],["--deadline-seconds","0"],["--seconds","301"]]:
            with self.assertRaises(SystemExit):scale.parse_args(argv)

    def test_commands_stay_in_own_project(self):
        command=scale.compose_command(Path("/repo"),Path("/repo/.local/scale/compose.env"))
        self.assertEqual(command[:4],["docker","compose","--project-name","meshops-scale"])
        self.assertNotIn("compose.demo.yml",command)
        self.assertIn("compose.scale.yml", " ".join(command))

    def test_failure_timeout_and_oom_precedence(self):
        self.assertEqual(scale.classify(False,[],0,{"stage":"complete","failure":""}),"passed")
        self.assertEqual(scale.classify(False,[],0,None),"missing_report")
        self.assertEqual(scale.classify(False,[],1,{"stage":"initialize","failure":"db failed"}),"failed")
        self.assertEqual(scale.classify(True,[],137,None),"timeout")
        self.assertEqual(scale.classify(True,[{"oomKilled":True}],137,None),"oom")
        self.assertEqual(scale.classify(False,[],1,{"failure":"under_offered: 42"}),"under_offered")
        self.assertEqual(scale.classify(False,[],0,{"stage":"warmup_publish"}),"incomplete")
        self.assertEqual(scale.classify(False,[{"cgroupOomKillCount":1}],1,None),"oom")

    def test_previous_runner_does_not_block_dependency_readiness(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner=scale.Runner(scale.parse_args([]),Path(tmp))
            commands=[]
            def command(cmd,**kwargs):
                commands.append(cmd)
                return subprocess.CompletedProcess(cmd,0,cmd[-1]+"-id\n","")
            runner.command=command
            runner.discover(include_runner=False)
            self.assertEqual(set(runner.ids),{"mysql","redis","kafka"})

    def test_secret_is_persistent_local_and_only_digest_reported(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/"mysql-root-password"
            digest=scale.prepare_secret(path)
            secret=path.read_text().strip()
            self.assertEqual(len(secret),64)
            self.assertEqual(digest,scale.prepare_secret(path))
            self.assertNotEqual(secret,digest)
            self.assertEqual(scale.redact("dsn password="+secret,secret),"dsn password=[REDACTED]")

    def test_inspect_template_is_field_allowlist(self):
        self.assertNotIn(".Config.Env",scale.INSPECT_FORMAT)
        for field in [".State.OOMKilled",".State.ExitCode",".HostConfig.Memory",".HostConfig.NanoCpus"]:
            self.assertIn(field,scale.INSPECT_FORMAT)

    def test_missing_cgroup_evidence_is_not_treated_as_zero_oom(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner=scale.Runner(scale.parse_args([]),Path(tmp))
            runner.ids={"runner":"runner-id"}
            runner.inspect=lambda:[{"id":"runner-id","running":True}]
            runner.command=Mock(side_effect=[
                subprocess.CompletedProcess([],1,"","unavailable"),
                subprocess.CompletedProcess([],0,"",""),
            ])
            runner.sample()
            self.assertEqual(runner.result.get("resourceSampleErrors"),1)

    def test_compose_has_no_ports_shared_volumes_or_unbounded_services(self):
        raw=(Path(__file__).parent.parent/"compose.scale.yml").read_text(encoding="utf-8")
        self.assertNotIn("ports:",raw)
        self.assertNotIn("external:",raw)
        self.assertIn("internal: true",raw)
        self.assertIn("mem_limit: 3g",raw)
        self.assertIn('cpus: "4"',raw)
        self.assertIn('"--maxmemory", "1gb"',raw)
        self.assertIn('"--maxmemory-policy", "noeviction"',raw)

    def test_initial_failure_saves_request_and_releases_lock(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner=scale.Runner(scale.parse_args(["--entities","1000000"]),Path(tmp))
            runner.command=Mock(side_effect=RuntimeError("docker unavailable"))
            self.assertEqual(runner.execute(),1)
            report=json.loads((runner.output/"summary.json").read_text(encoding="utf-8"))
            self.assertEqual(report["requested"]["entities"],1000000)
            self.assertEqual(report["outcome"],"failed")
            self.assertFalse((runner.local/"run.lock").exists())

    def test_timeout_stops_only_own_dependencies_and_retains_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner=scale.Runner(scale.parse_args([]),Path(tmp))
            calls=[]
            def command(cmd,**kwargs):
                calls.append(cmd)
                if "version" in cmd:return subprocess.CompletedProcess(cmd,0,'{}','')
                if "up" in cmd:raise subprocess.TimeoutExpired(cmd,1)
                return subprocess.CompletedProcess(cmd,0,'','')
            runner.command=command
            self.assertEqual(runner.execute(),1)
            self.assertEqual(runner.result["outcome"],"timeout")
            self.assertTrue(any("stop" in cmd for cmd in calls))
            self.assertFalse(any("down" in cmd or "--volumes" in cmd for cmd in calls))
            for cmd in calls:
                if "stop" in cmd or "kill" in cmd:self.assertEqual(cmd[:4],["docker","compose","--project-name","meshops-scale"])

if __name__=="__main__":unittest.main()
