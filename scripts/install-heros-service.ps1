param(
  [string]$Name = "Heros",
  [string]$Binary = "$PSScriptRoot\..\heros.exe",
  [string]$Config = "$env:ProgramData\Heros\config.json"
)

$Binary = [System.IO.Path]::GetFullPath($Binary)
if (-not (Test-Path $Binary)) { throw "binary not found: $Binary" }

New-Item -ItemType Directory -Force -Path (Split-Path $Config) | Out-Null

if (Get-Service -Name $Name -ErrorAction SilentlyContinue) {
  Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
  sc.exe delete $Name | Out-Null
}

New-Service -Name $Name -BinaryPathName "`"$Binary`" -config `"$Config`"" -DisplayName $Name -StartupType Automatic | Out-Null
Start-Service -Name $Name
