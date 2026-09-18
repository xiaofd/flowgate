# live-test.ps1 —— 提权运行 flowgate 一段时间后自动停止（真实 TUN/WARP 链路验证）
# 用法（由非管理员终端触发，会弹 UAC）:
#   Start-Process -Verb RunAs powershell -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','<本脚本>'
param(
    [int]$Seconds = 40,
    [string]$HomeDir = ""
)

if (-not $HomeDir) { $HomeDir = Join-Path $PSScriptRoot "..\.fg-home" }

$trace = Join-Path $HomeDir "logs\live-test.trace.log"
New-Item -ItemType Directory -Force -Path (Split-Path $trace) | Out-Null
$fg = Join-Path $PSScriptRoot "..\build\flowgate.exe"
"=== start $(Get-Date) fg=$fg exists=$(Test-Path $fg) ===" | Out-File $trace -Append

# 直接前台运行，由 flowgate 自带的 --run-seconds 优雅自停
$env:FG_HOME = $HomeDir
& $fg run --no-elevate --run-seconds $Seconds 2>&1 | Out-File $trace -Append

"=== end $(Get-Date) exit=$LASTEXITCODE ===" | Out-File $trace -Append
