# 先加载后端环境，再启用个人账号；初始管理员通过 account-admin.ps1 自行设置密码。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
$env:MESHOPS_ACCOUNT_AUTH = '1'
$env:MESHOPS_MANIFEST = Join-Path $courseRoot 'configs/simulation.yaml'
$env:MESHOPS_WEB_TENANT = 'demo_tenant'
$env:MESHOPS_WEB_LISTEN = '127.0.0.1:18090'
$env:MESHOPS_SEARCH_ENDPOINT = '127.0.0.1:50055'
$env:MESHOPS_WEB_ORIGINS = 'http://localhost:18090,http://127.0.0.1:18090,http://localhost:5173,http://127.0.0.1:5173'
