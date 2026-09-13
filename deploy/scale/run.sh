#!/bin/sh
# 使用既有成品二进制与真实验证器，口令仅在进程内展开。
set -eu
umask 077
mkdir -p /work/bin /work/.local/verification
for name in verify ingest entity; do
    test -x "/app/$name"
    ln -sf "/app/$name" "/work/bin/$name"
done
ln -sfn /app/migrations /work/migrations
ln -sfn /app/testdata /work/testdata
password=$(cat /run/secrets/mysql-root-password)
export MESHOPS_MYSQL_DSN="root:${password}@tcp(mysql:3306)/mysql?parseTime=true&multiStatements=true&timeout=5s&readTimeout=30s&writeTimeout=30s"
unset password
cd /work
exec /app/verify --mode benchmark "$@"
