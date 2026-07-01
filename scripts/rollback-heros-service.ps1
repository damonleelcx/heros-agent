param(
  [string]$Name = "Heros",
  [string]$InstallDir = "$env:ProgramData\Heros",
  [string]$BackupPath = ""
)

$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$Current = Join-Path $InstallDir "heros.exe"

if (-not (Test-Path $Current)) { throw "installed binary not found: $Current" }

if ([string]::IsNullOrWhiteSpace($BackupPath)) {
  $Backup = Get-ChildItem -LiteralPath $InstallDir -Filter "heros.exe.bak-*" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
  if (-not $Backup) { throw "no backup found in $InstallDir" }
  $BackupPath = $Backup.FullName
}

if (-not (Test-Path $BackupPath)) { throw "backup not found: $BackupPath" }

if (Get-Service -Name $Name -ErrorAction SilentlyContinue) {
  Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
}

Copy-Item -LiteralPath $BackupPath -Destination $Current -Force

if (Get-Service -Name $Name -ErrorAction SilentlyContinue) {
  Start-Service -Name $Name
}

Write-Host "rolled back $Name from $BackupPath"
