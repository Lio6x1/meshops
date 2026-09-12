"""Check explicit integration acceptance, including tests that silently skipped.

Usage: python scripts/check-test-results.py results/integration.jsonl
Ordinary unit runs intentionally omit external dependencies and must not use this
gate. Subprocess entry points are helpers; their parent crash tests must pass.
"""
import json
import sys
from pathlib import Path


REQUIRED = {
    "internal/bus": {"TestPartitionFailureDoesNotBlockHealthyPartition", "TestRetentionGapIsNotReportedAsHealthyLag"},
    "internal/state": {"TestSixFixturesThroughAuthenticatedRPCAndKafka", "TestRedisSubscriptionInitialRaceDeleteAndGap", "TestGRPCBlockedSubscriberReleasesSenderAndDoesNotBlockPeer", "TestRedisOOMDoesNotCommitKafkaOffset", "TestRedisForceKillBeforeOffsetReplaysWithoutRegressing", "TestMySQLSampleIdempotencePagingAgeAndBudget", "TestNewEntityAndRunStrictRecoveryLifecycle"},
    "internal/edge": {"TestGatewayForceKillAfterRemoteACKBeforeLocalCommit", "TestExecutorForceKillRetainsSingleEffectAndExactReport"},
    "internal/tasks": {"TestA23DurableDLQAndConcurrentManualRetry", "TestOutboxAndDispatcherForceKillDurableBoundaries", "TestReportRequiresDurableDispatchIntent", "TestDispatchWorkerPreservesNewerTerminalMirror", "TestListTasksCountAndPageShareSnapshot"},
    "internal/search": {"TestRealElasticsearchVersionProjection", "TestSnapshotEstablishesReadViewBeforeReleasingLock", "TestCDCRebuildStartsAfterOldHistory"},
}


def check(path):
    events = [json.loads(line) for line in Path(path).read_text(encoding="utf-8-sig").splitlines() if line.strip()]
    passed = {(e.get("Package", "").removeprefix("example.com/meshops-course/"), e.get("Test")) for e in events if e.get("Action") == "pass"}
    missing = [(package, name) for package, names in REQUIRED.items() for name in names if (package, name) not in passed]
    # 仅允许下列精确匹配的辅助入口跳过；跳过子测试或其他测试，
    # 都表示本次显式验收不完整，不能算作成功。
    helpers = {"example.com/meshops-course/internal/" + p for p in ("edge", "state", "tasks")}
    # Go 对没有测试文件的包也会输出包级 skip 事件，
    # 例如生成的 protobuf 包；这不等于跳过某个测试用例。
    # 关键包缺少必跑测试时，仍会被上面的 REQUIRED 检查拒绝。
    skipped = [(e.get("Package"), e.get("Test")) for e in events if e.get("Action") == "skip" and e.get("Test") and not (e.get("Package") in helpers and e.get("Test") == "TestDurableCrashChild")]
    failed = [(e.get("Package"), e.get("Test")) for e in events if e.get("Action") in {"fail", "build-fail"}]
    if missing or skipped or failed:
        raise SystemExit(f"Incomplete integration acceptance: missing={sorted(missing)}, skipped={skipped}, failed={failed}")
    print(f"Integration gate passed: {sum(map(len, REQUIRED.values()))} required tests executed; no business-test skips.")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: python scripts/check-test-results.py <go-test-jsonl>")
    check(sys.argv[1])
