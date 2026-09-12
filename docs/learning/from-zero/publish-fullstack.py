"""Publish Z11 HTTP, Z12 browser and Z13 complete deployment from one implementation.

Z10 is the frozen pre-browser baseline. Run only after feature files are complete.
This creates teaching source snapshots, never a user's learner directory.
"""
from pathlib import Path
import hashlib
import json

MATERIAL = Path(__file__).resolve().parent
ROOT = MATERIAL.parents[2]
INDEX = MATERIAL / 'checkpoint-index.json'
EXCLUDED = {'node_modules', 'dist', '.cache', '.npm-cache', '.local', 'test-results', 'playwright-report', '__pycache__'}

def runtime_paths():
    paths = {'.gitattributes', '.gitignore', 'go.mod', 'go.sum', 'README.md', 'docker-compose.yml', 'compose.search.yml', 'compose.demo.yml', 'Dockerfile', '.dockerignore'}
    for directory in ('.github', 'cmd', 'configs', 'gen', 'internal', 'migrations', 'proto', 'scripts', 'testdata', 'deploy', 'web'):
        for p in (ROOT / directory).rglob('*'):
            rel = p.relative_to(ROOT)
            if not p.is_file() or any(part in EXCLUDED for part in rel.parts) or p.suffix in ('.exe', '.pyc', '.log'):
                continue
            if p.name == 'integration-results.txt':
                continue
            if p.is_symlink():
                raise ValueError(f'Linked source {rel}')
            paths.add(rel.as_posix())
    return paths

def main():
    index = json.loads(INDEX.read_text(encoding='utf-8-sig'))
    stages = [s for s in index['stages'] if s['stage'] <= 'z10']
    baseline = next(s for s in stages if s['stage'] == 'z10')
    assert baseline['source'] == 'business-stages/z10'
    base = MATERIAL / baseline['source']
    source = {f['path']: (base / f['path']).read_bytes() for f in baseline['files']}
    for f in baseline['files']:
        assert hashlib.sha256(source[f['path']]).hexdigest() == f['sha256']
    runtime = runtime_paths() | {p for p in source if p.startswith('verification/')}
    missing = [p for p in runtime if not (ROOT / p).is_file()]
    if missing:
        raise ValueError(f'Incomplete runtime: {missing}')
    browser = {p for p in runtime if p.startswith('web/')} | {'scripts/frontend.ps1'}
    gateway = {p for p in runtime if p.startswith(('internal/web/', 'cmd/web-gateway/', 'internal/simulation/')) or p.endswith('.pb.gw.go')}
    gateway |= {'go.mod', 'go.sum', '.gitignore', 'proto/http.yaml', 'scripts/build.ps1', 'scripts/generate-http.ps1', 'scripts/verify-proto.ps1', 'scripts/web-env.ps1', 'scripts/start-web.ps1', 'scripts/stop-web.ps1'}
    previous = {f['path']: f['sha256'] for f in baseline['files']}
    for stage_id, additions in (('z11', gateway), ('z12', browser), ('z13', runtime)):
        if stage_id == 'z13':
            source = {p: (ROOT / p).read_bytes() for p in runtime}
            stage_source = '../../..'
        else:
            source.update({p: (ROOT / p).read_bytes() for p in additions})
            stage_source = f'web-stages/{stage_id}'
            target = MATERIAL / stage_source
            # A removed previous publication must be explicitly reviewed, not silently deleted.
            existing = {p.relative_to(target).as_posix() for p in target.rglob('*') if p.is_file()}
            if existing - source.keys():
                raise ValueError(f'Stale teaching files require review: {existing - source.keys()}')
            for p, data in source.items():
                destination = target / p
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(data)
        current = {p: hashlib.sha256(data).hexdigest() for p, data in source.items()}
        stages.append({'stage': stage_id, 'source': stage_source,
                       'files': [{'path': p, 'sha256': current[p]} for p in sorted(current)],
                       'changes': {'added': sorted(current.keys() - previous.keys()),
                                   'removed': sorted(previous.keys() - current.keys()),
                                   'replaced': sorted(p for p in current.keys() & previous.keys() if current[p] != previous[p])}})
        previous = current
        print(f'{stage_id}: {len(current)} files')
    index['stages'] = stages
    INDEX.write_text(json.dumps(index, ensure_ascii=False, indent=2) + '\n', encoding='utf-8', newline='\n')

if __name__ == '__main__':
    main()
