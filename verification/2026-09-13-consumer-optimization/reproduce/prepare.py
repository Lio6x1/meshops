from pathlib import Path
import io, tarfile, subprocess, shutil

repo=Path.cwd(); here=repo/'.cache/consumer-opt'; here.mkdir(exist_ok=True)
paths=['cmd','internal','gen','configs','migrations','testdata','deploy','proto','Dockerfile','.dockerignore','go.mod','go.sum','compose.scale.yml','docker-compose.yml','compose.search.yml','compose.demo.yml','README.md']
base=here/'baseline';base.mkdir(exist_ok=True)
raw=subprocess.check_output(['git','archive','0f9589a5a80e98f40ba6ee4a3a0450511918c1fb','--',*paths])
with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
    for member in archive:
        if not member.isfile():continue
        target=(base/member.name).resolve()
        assert target.is_relative_to(base.resolve())
        target.parent.mkdir(parents=True,exist_ok=True)
        target.write_bytes(archive.extractfile(member).read())
bus=['internal/bus/kafka.go','internal/bus/commit_batch.go']
state=['internal/state/entity.go','internal/state/history.go','internal/state/history_budget.go']
for name,changed in [('baseline',[]),('batch',bus),('both',bus)]:
    dest=here/name
    if name!='baseline':shutil.copytree(base,dest,dirs_exist_ok=True)
    for relative in changed:shutil.copy2(repo/relative,dest/relative)
    if name=='both':subprocess.run(['git','apply','--directory='+dest.relative_to(repo).as_posix(),str(here/'history-parallel.patch')],check=True)
    p=dest/'compose.scale.yml';p.write_text(p.read_text(encoding='utf-8').replace('mysqladmin ping -h localhost --silent','mysqladmin ping -h 127.0.0.1 --silent'),encoding='utf-8')
    subprocess.run(['docker','build','--target','backend','-t','meshops-consumer-opt:'+name,str(dest)],check=True)
print('BUILDS_COMPLETE',flush=True)
