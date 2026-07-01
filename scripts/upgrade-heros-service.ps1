param(
  [string]$Name = "Heros",
  [string]$Binary = "$PSScriptRoot\..\heros.exe",
  [string]$InstallDir = "$env:ProgramData\Heros",
  [switch]$SkipServiceRestart
)

$Binary = [System.IO.Path]::GetFullPath($Binary)
if (-not (Test-Path $Binary)) { throw "binary not found: $Binary" }

$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

$Current = Join-Path $InstallDir "heros.exe"
$Backup = Join-Path $InstallDir ("heros.exe.bak-" + (Get-Date).ToString("yyyyMMddHHmmss"))

if (Test-Path $Current) {
  Copy-Item -LiteralPath $Current -Destination $Backup -Force
}

Copy-Item -LiteralPath $Binary -Destination $Current -Force

if (-not $SkipServiceRestart) {
  if (Get-Service -Name $Name -ErrorAction SilentlyContinue) {
    Restart-Service -Name $Name -Force
  }
}

Write-Host "upgraded $Name -> $Current"
Write-Host "backup: $Backup"
