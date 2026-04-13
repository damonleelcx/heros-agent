# Install heros and add Go's bin dir to user PATH (same as: go install; heros -add-path).
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
go install ./cmd/heros
$Bin = Join-Path (go env GOPATH) "bin"
$Exe = Join-Path $Bin "heros.exe"
if (-not (Test-Path $Exe)) {
    Write-Error "Expected $Exe after go install"
}
& $Exe -add-path
