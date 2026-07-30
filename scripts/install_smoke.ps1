<#
.SYNOPSIS
  Fresh-machine smoke matrix and tamper red-check for scripts/install.ps1 (P20 tasks 3.2, 6.3, 7.2, 7.3).

.DESCRIPTION
  The Windows counterpart of scripts/install_smoke.py, and it exists for the same reason: install.ps1's most
  important behaviour is a REFUSAL, and a refusal that has never been observed is treated as absent.

  ── What it can prove without the release signing key ────────────────────────────────────────────────────

  All four refusals. Each case stages a local fixture release, serves it over loopback HTTP, runs the real
  install.ps1 against it, and asserts BOTH a non-zero exit AND that no heros.exe was left behind. Either alone
  is a bug: an exit code nobody checks, or a binary a later PATH lookup finds.

    tampered-binary       the manifest still holds the original checksum   -> the checksum step must catch it
    tampered-manifest     the manifest matches the substituted bytes       -> only the SIGNATURE can catch it
    unsigned-release      no .sshsig at all                                -> must refuse, not fall back
    foreign-signing-key   a valid signature from the wrong key             -> must refuse

  ── What needs the key, and is therefore conditional ─────────────────────────────────────────────────────

  The happy path. install.ps1 pins the real release public key, so a fixture it accepts must be signed by the
  real private key. With $env:HEROS_RELEASE_PRIVATE_KEY set the happy path runs too; without it, the script
  says so in its report rather than quietly reporting a smaller matrix as a complete one.

.PARAMETER Version
  The fixture release version. Defaults to a smoke-specific prerelease so it can never be mistaken for a real one.
#>
[CmdletBinding()]
param(
    [string]$Version = "0.20.0-smoke.1"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("heros-ps-smoke-" + [guid]::NewGuid().ToString('N'))
$fixture = Join-Path $work 'fixture'
$installDir = Join-Path $work 'bin'
New-Item -ItemType Directory -Force -Path $fixture, $installDir | Out-Null

$asset = "heros-$Version-windows-amd64.exe"
$results = New-Object System.Collections.ArrayList

function Add-Result([string]$Case, [bool]$Ok, [string]$Detail) {
    [void]$results.Add([pscustomobject]@{ Case = $Case; Ok = $Ok; Detail = $Detail })
    $mark = if ($Ok) { '[ok]' } else { '[XX]' }
    Write-Host "  $mark $Case — $Detail"
}

# ── the binary under test ───────────────────────────────────────────────────────────────────────────────
# Built through the SAME script the release uses. A smoke test that built the binary its own way would prove
# something about a build nobody ships.
Write-Host "building the native windows binary through scripts/release-cli.sh"
Push-Location $repoRoot
try {
    $env:GOWORK = 'off'
    $env:OUT = (Join-Path $work 'built')
    New-Item -ItemType Directory -Force -Path $env:OUT | Out-Null
    & bash scripts/release-cli.sh $Version
    if ($LASTEXITCODE -ne 0) { throw "release-cli.sh failed" }
} finally {
    Pop-Location
}
$builtAsset = Join-Path $env:OUT $asset
if (-not (Test-Path $builtAsset)) { throw "expected $builtAsset to exist after the build" }

# ── the fixture server ──────────────────────────────────────────────────────────────────────────────────
# A HttpListener rather than a real download host: the point is to control the BYTES, and a tamper case cannot
# be staged against a server somebody else owns.
$listener = New-Object System.Net.HttpListener
$port = Get-Random -Minimum 34000 -Maximum 44000
$prefix = "http://127.0.0.1:$port/"
$listener.Prefixes.Add($prefix)
$listener.Start()

$serverJob = Start-Job -ScriptBlock {
    param($prefix, $root)
    $l = New-Object System.Net.HttpListener
    $l.Prefixes.Add($prefix)
    $l.Start()
    while ($l.IsListening) {
        try {
            $ctx = $l.GetContext()
            $rel = $ctx.Request.Url.AbsolutePath.TrimStart('/')
            $path = Join-Path $root $rel
            if (Test-Path -PathType Leaf $path) {
                $bytes = [IO.File]::ReadAllBytes($path)
                $ctx.Response.StatusCode = 200
                $ctx.Response.OutputStream.Write($bytes, 0, $bytes.Length)
            } else {
                $ctx.Response.StatusCode = 404
            }
            $ctx.Response.Close()
        } catch { break }
    }
} -ArgumentList $prefix, $fixture
$listener.Stop()   # the job owns the listener; this instance only reserved the port

Start-Sleep -Milliseconds 500

function Set-Fixture {
    param(
        [switch]$TamperBinary,
        [switch]$TamperManifest,
        [switch]$DropSignature,
        [switch]$ForeignKey
    )
    $dl = Join-Path $fixture "download/v$Version"
    if (Test-Path $dl) { Remove-Item -Recurse -Force $dl }
    New-Item -ItemType Directory -Force -Path $dl, (Join-Path $fixture 'api') | Out-Null
    Copy-Item $builtAsset (Join-Path $dl $asset) -Force

    # The manifest the release SIGNED is always the one over the original bytes. Everything below models an
    # attacker who controls what is served but not the signing key — the only threat model in which the
    # signature earns its place.
    $signedManifest = "{0}  {1}`n" -f (Get-FileHash -Algorithm SHA256 (Join-Path $dl $asset)).Hash.ToLower(), $asset
    $servedManifest = $signedManifest

    if ($TamperBinary) {
        $bytes = [IO.File]::ReadAllBytes((Join-Path $dl $asset))
        $bytes[[int]($bytes.Length / 2)] = $bytes[[int]($bytes.Length / 2)] -bxor 1
        [IO.File]::WriteAllBytes((Join-Path $dl $asset), $bytes)
    }
    if ($TamperManifest) {
        # The attacker rewrites the manifest to match the bytes they substituted: the checksum step now PASSES
        # and only the signature can catch it. This is the case that answers "why sign when you publish sums".
        $servedManifest = "{0}  {1}`n" -f (Get-FileHash -Algorithm SHA256 (Join-Path $dl $asset)).Hash.ToLower(), $asset
    }
    [IO.File]::WriteAllText((Join-Path $dl 'SHA256SUMS'), $servedManifest, [Text.Encoding]::ASCII)

    if (-not $DropSignature) {
        $key = $env:HEROS_RELEASE_PRIVATE_KEY
        if ($ForeignKey -or -not $key) {
            $gen = & go run ./cmd/herossign keygen
            $key = ($gen | Where-Object { $_ -like 'HEROS_RELEASE_PRIVATE_KEY=*' }) -replace '^HEROS_RELEASE_PRIVATE_KEY=', ''
        }
        $signedPath = Join-Path $dl '.signed-manifest'
        [IO.File]::WriteAllText($signedPath, $signedManifest, [Text.Encoding]::ASCII)
        $prev = $env:HEROS_RELEASE_PRIVATE_KEY
        $env:HEROS_RELEASE_PRIVATE_KEY = $key
        try {
            $sig = & go run ./cmd/herossign sign --ssh --in $signedPath
            [IO.File]::WriteAllText((Join-Path $dl 'SHA256SUMS.sshsig'), ($sig -join "`n") + "`n", [Text.Encoding]::ASCII)
        } finally {
            $env:HEROS_RELEASE_PRIVATE_KEY = $prev
            Remove-Item -Force $signedPath
        }
    }
    [IO.File]::WriteAllText((Join-Path $fixture 'api/latest'), "{`"tag_name`":`"v$Version`"}", [Text.Encoding]::ASCII)
}

function Invoke-Install {
    $env:HEROS_RELEASE_BASE_URL = "http://127.0.0.1:$port"
    $env:HEROS_RELEASE_API_URL = "http://127.0.0.1:$port/api"
    $env:HEROS_INSTALL_DIR = $installDir
    $exe = Join-Path $installDir 'heros.exe'
    if (Test-Path $exe) { Remove-Item -Force $exe }
    $out = & pwsh -NoProfile -File (Join-Path $repoRoot 'scripts/install.ps1') 2>&1
    return [pscustomobject]@{ Exit = $LASTEXITCODE; Output = ($out -join "`n"); Installed = (Test-Path $exe) }
}

Push-Location $repoRoot
try {
    Write-Host ""
    Write-Host "== refusals (no release key needed) =="

    Set-Fixture -TamperBinary
    $r = Invoke-Install
    Add-Result 'tampered-binary' (($r.Exit -ne 0) -and (-not $r.Installed)) "exit=$($r.Exit) installed=$($r.Installed)"

    Set-Fixture -TamperBinary -TamperManifest
    $r = Invoke-Install
    Add-Result 'tampered-manifest' (($r.Exit -ne 0) -and (-not $r.Installed)) "exit=$($r.Exit) installed=$($r.Installed)"

    Set-Fixture -DropSignature
    $r = Invoke-Install
    Add-Result 'unsigned-release' (($r.Exit -ne 0) -and (-not $r.Installed)) "exit=$($r.Exit) installed=$($r.Installed)"

    Set-Fixture -ForeignKey
    $r = Invoke-Install
    Add-Result 'foreign-signing-key' (($r.Exit -ne 0) -and (-not $r.Installed)) "exit=$($r.Exit) installed=$($r.Installed)"

    Write-Host ""
    if ($env:HEROS_RELEASE_PRIVATE_KEY) {
        Write-Host "== happy path (release key present) =="
        Set-Fixture
        $r = Invoke-Install
        $reported = ''
        if ($r.Installed) {
            $json = & (Join-Path $installDir 'heros.exe') version 2>$null | Out-String
            if ($json -match '"tool_version": "([^"]+)"') { $reported = $Matches[1] }
        }
        Add-Result 'happy-path' (($r.Exit -eq 0) -and $r.Installed -and ($reported -eq $Version)) "exit=$($r.Exit) reported=$reported"

        Set-Fixture
        $env:HEROS_VERSION = $Version
        $env:HEROS_RELEASE_API_URL = 'http://127.0.0.1:9/deliberately-unreachable'
        $r = Invoke-Install
        Remove-Item Env:\HEROS_VERSION
        Add-Result 'pinned-version-no-api' (($r.Exit -eq 0) -and $r.Installed) "exit=$($r.Exit)"

        $env:HEROS_UNINSTALL = '1'
        $u = & pwsh -NoProfile -File (Join-Path $repoRoot 'scripts/install.ps1') 2>&1
        $ucode = $LASTEXITCODE
        Remove-Item Env:\HEROS_UNINSTALL
        Add-Result 'uninstall' (($ucode -eq 0) -and (-not (Test-Path (Join-Path $installDir 'heros.exe')))) "exit=$ucode"
    } else {
        Write-Host "== happy path SKIPPED — HEROS_RELEASE_PRIVATE_KEY is not set =="
        Write-Host "   install.ps1 pins the real release public key, so a fixture it ACCEPTS must be signed by the"
        Write-Host "   real private key. The four refusals above needed no key and all ran. This is stated rather"
        Write-Host "   than silently reporting a smaller matrix as a complete one."
    }
} finally {
    Pop-Location
    Stop-Job $serverJob -ErrorAction SilentlyContinue | Out-Null
    Remove-Job $serverJob -Force -ErrorAction SilentlyContinue | Out-Null
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host ("=" * 78)
$failed = @($results | Where-Object { -not $_.Ok })
foreach ($r in $results) {
    $mark = if ($r.Ok) { '[ok]' } else { '[XX]' }
    Write-Host ("  {0} {1,-24} {2}" -f $mark, $r.Case, $r.Detail)
}
Write-Host ("=" * 78)
if ($failed.Count -gt 0) {
    Write-Host "$($failed.Count) of $($results.Count) cases FAILED"
    exit 1
}
Write-Host "all $($results.Count) cases passed"
