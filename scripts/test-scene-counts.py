"""Bounded six-type demo acceptance; preserves original count/mode settings.

Uses only the loopback HTTP API. It creates four inspect tasks and keeps their
history as evidence. Passwords and cookies are never written to the report.
"""
import datetime as dt
import importlib.util
import json
import os
from pathlib import Path
import sys
import urllib.error
import uuid

spec = importlib.util.spec_from_file_location('simulation_acceptance', Path(__file__).with_name('test-simulation.py'))
sim = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sim)


def sources():
    return sim.api('/api/v1/simulation')['sources']


def set_count(source, count):
    sim.api('/api/v1/simulation/' + source + '/count', 'PUT', {'count': count})


def all_applied(count):
    rows = sources()
    return rows if len(rows) == 6 and all(s['connected'] and s['observation']['activeCount'] == count and s['observation']['applied'] == 'running' for s in rows) else None


def main():
    output = Path(sys.argv[1])
    if output.exists():
        raise ValueError('Evidence output already exists')
    sim.login()
    original = sources()
    report = {'passed': False, 'checks': sim.checks, 'scope': '30 real mixed entity projections, four new executor bindings, count bounds/shrink/zero and retained history. This is not a load benchmark.'}
    try:
        assert len(original) == 6
        for source in original:
            assert len(source['entityIds']) == 5
            set_count(source['sourceId'], 5)
            sim.api('/api/v1/simulation/' + source['sourceId'], 'PUT', {'mode': 'running'})
        sim.wait_for(lambda: all_applied(5), 25)
        inventory = sim.api('/api/v1/entities')['entities']
        assert len(inventory) == 30
        types = {kind: [e for e in inventory if e['entityType'] == kind] for kind in ('person', 'drone', 'vehicle', 'robot', 'sensor', 'facility')}
        assert all(len(rows) == 5 for rows in types.values())
        evidence = []
        for entity in inventory:
            def fresh():
                value = sim.api('/api/v1/entities/' + entity['entityId'])
                if not value.get('found') or not value.get('expiresAt'):
                    return None
                return value if dt.datetime.fromisoformat(value['expiresAt'].replace('Z', '+00:00')) > dt.datetime.now(dt.timezone.utc) else None
            value = sim.wait_for(fresh, 25)
            assert value['snapshot']['entityType'] == entity['entityType']
            assert value['snapshot'].get('location')
            if entity['entityType'] in ('drone', 'robot'):
                assert 'batteryPercent' in value['snapshot']['power']
            evidence.append({'entityId': entity['entityId'], 'entityType': entity['entityType'], 'version': value['version']})
        sim.check('30 fresh snapshots across six real entity types', evidence)
        assert sum(bool(e.get('executorId')) for e in inventory) == 20
        sim.check('20 capable bindings and 10 observation-only bindings', {'executing': 20, 'observationOnly': 10})
        task_ids = []
        for kind in ('person', 'drone', 'vehicle', 'robot'):
            entity_id = kind + '-005'
            body = {'idempotencyKey': str(uuid.uuid4()), 'taskType': 'inspect', 'targetEntityId': entity_id, 'priority': 5, 'deadline': (dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)).isoformat(), 'payload': {'payloadJson': json.dumps({'duration_seconds': 1, 'note': 'mixed-scene-count-acceptance'})}}
            task_id = sim.api('/api/v1/tasks', 'POST', body)['taskId']
            task_ids.append(task_id)
            def completed():
                task = sim.api('/api/v1/tasks/' + task_id)['task']
                if task['status'] in ('TASK_STATUS_FAILED', 'TASK_STATUS_CANCELLED', 'TASK_STATUS_TIMED_OUT', 'TASK_STATUS_REJECTED'):
                    raise AssertionError('Inspection reached an unexpected terminal status')
                return task if task['status'] == 'TASK_STATUS_SUCCEEDED' else None
            value = sim.wait_for(completed, 45)
            sim.check(kind + '-005 inspect succeeds', {'entityId': entity_id, 'taskId': task_id, 'status': value['status']})
        try:
            set_count('drone_sim', 6)
            raise AssertionError('Count 6 accepted')
        except urllib.error.HTTPError as exc:
            assert exc.code == 400
        sim.check('server rejects count above five', {'httpStatus': 400})
        set_count('drone_sim', 2)
        assert len(sim.api('/api/v1/entities')['entities']) == 27
        set_count('drone_sim', 0)
        assert len(sim.api('/api/v1/entities')['entities']) == 25
        assert sim.api('/api/v1/entities/drone-005')['found']
        assert sim.api('/api/v1/tasks/' + task_ids[1])['task']['status'] == 'TASK_STATUS_SUCCEEDED'
        sim.check('shrink hides inactive IDs and retains snapshot/task', {'afterTwo': 27, 'afterZero': 25})
        for source in original:
            set_count(source['sourceId'], 0)
        sim.wait_for(lambda: all_applied(0), 20)
        assert sim.api('/api/v1/entities')['entities'] == []
        sim.check('all zero produces an empty active scene', {'entities': 0})
        report['passed'] = True
    finally:
        restored = []
        try:
            # 某个来源恢复失败时，仍要尝试恢复其余全部来源；
            # 不能用成功报告掩盖恢复不完整。
            for source in original:
                try:
                    set_count(source['sourceId'], source['count'])
                    sim.api('/api/v1/simulation/' + source['sourceId'], 'PUT', {'mode': source['desired']})
                    restored.append(source['sourceId'])
                except Exception:
                    report['passed'] = False
            report['restoredSources'] = restored
            if len(restored) != len(original):
                raise RuntimeError('Some source settings were not restored; inspect simulation page')
        finally:
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
            sim.api('/api/session', 'DELETE')


if __name__ == '__main__':
    main()
