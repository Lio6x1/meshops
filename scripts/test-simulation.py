"""Real loopback demo acceptance. Credentials are read from environment, never printed.

Uses the running drone simulator and always restores its previous desired mode.
No Docker control, queue deletion or database reset. Run after demo-stack Up.
"""
import datetime as dt
import http.cookiejar
import json
import os
from pathlib import Path
import sys
import time
import urllib.request

BASE = 'http://127.0.0.1:18090'
checks = []
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
csrf = ''

def api(path, method='GET', body=None):
    headers = {'Origin': BASE, 'Content-Type': 'application/json', 'X-CSRF-Token': csrf}
    request = urllib.request.Request(BASE + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    with opener.open(request, timeout=12) as response:
        return json.loads(response.read() or b'null')

def source():
    return next(s for s in api('/api/v1/simulation')['sources'] if s['sourceId'] == 'drone_sim')

def wait_for(predicate, seconds=15):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = predicate()
        if value:
            return value
        time.sleep(.6)
    raise AssertionError('Timed out waiting for expected simulator state')

def applied(value):
    s = source()
    return s if s['connected'] and s['observation']['applied'] == value else None

def set_mode(value):
    api('/api/v1/simulation/drone_sim', 'PUT', {'mode': value})
    return wait_for(lambda: applied(value))

def snapshot():
    return api('/api/v1/entities/drone-001')

def check(name, details):
    checks.append({'name': name, 'passed': True, 'details': details})
    print('PASS:', name, flush=True)

def main():
    global csrf
    output = Path(sys.argv[1])
    if output.exists():
        raise ValueError('Evidence output already exists')
    code = os.environ['MESHOPS_WEB_OPERATOR_CODE']
    session = api('/api/session', 'POST', {'role': 'operator', 'accessCode': code})
    csrf = session['csrfToken']
    original = source()['desired']
    report = {'passed': False, 'checks': checks, 'scope': 'Real demo source control, durable pending queue and HTTP entity projection. Restores previous desired mode.'}
    try:
        inventory = api('/api/v1/simulation')['sources']
        assert len(inventory) == 6 and all(s['connected'] for s in inventory)
        check('six live source controllers', [s['sourceId'] for s in inventory])
        set_mode('running')
        first = snapshot()
        changed = wait_for(lambda: (s if (s := snapshot())['version'] != first['version'] else None))
        assert changed['snapshot']['location'] != first['snapshot']['location']
        check('running produces changing location and version', {'from': first['version'], 'to': changed['version']})
        paused = set_mode('paused')['observation']
        time.sleep(2)
        still = source()['observation']
        assert still['generated'] == paused['generated'] and still['sent'] == paused['sent']
        check('pause stops generation and sending', {'generated': still['generated'], 'sent': still['sent']})
        offline = set_mode('offline')['observation']
        growing = wait_for(lambda: (s if int((s := source())['observation']['pending']) > int(offline['pending']) + 3 else None))
        assert growing['observation']['sent'] == offline['sent']
        check('offline accumulates durable backlog without sends', {'pending': growing['observation']['pending']})
        # More than configured stale_after=30s; final in-flight batch may project late.
        time.sleep(34)
        old = snapshot()
        expires = dt.datetime.fromisoformat(old['expiresAt'].replace('Z', '+00:00'))
        assert expires < dt.datetime.now(dt.timezone.utc)
        check('last position visibly expires while source disconnected', {'version': old['version'], 'expiresAt': old['expiresAt']})
        set_mode('running')
        drained = wait_for(lambda: (s if int((s := source())['observation']['pending']) <= 2 else None), 30)
        fresh = wait_for(lambda: (s if (s := snapshot())['version'] != old['version'] and dt.datetime.fromisoformat(s['expiresAt'].replace('Z', '+00:00')) > dt.datetime.now(dt.timezone.utc) else None), 20)
        check('recovery drains queue and refreshes projection', {'pending': drained['observation']['pending'], 'version': fresh['version']})
        report['passed'] = True
    finally:
        try:
            set_mode(original)
            report['restoredMode'] = original
        finally:
            api('/api/session', 'DELETE')
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')

if __name__ == '__main__':
    main()
