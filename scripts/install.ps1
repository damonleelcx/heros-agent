<#
.SYNOPSIS
  Installs the heros CLI on Windows: detect -> download -> VERIFY -> place on PATH.

.DESCRIPTION
  irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.22.0/scripts/install.ps1 | iex

  Environment:
    HEROS_VERSION      install a specific version instead of the latest (this is the rollback, task 3.8)
    HEROS_INSTALL_DIR  where to place heros.exe (default: $env:LOCALAPPDATA\Programs\heros)
    HEROS_UNINSTALL=1  remove an installed heros and exit

  ── Why this script verifies for you, and why it stops instead of continuing ────────────────────────────

  The heros binary runs inside CI with repository access, so a compromised release compromises every
  customer's build. The likeliest real-world failure is a user, or a wrapper script, that installs and never
  runs the verify step — so the installer runs it, and on any failure it STOPS. There is no
  "install anyway, unverified" path: an installer with that fallback has traded security for the convenience
  of not handling the error case.

  Nothing reaches PATH until every check passes, and the download lives in a temp directory that is removed
  on exit either way, so a refused install leaves nothing behind for someone to find and run later.

  ── Why it does not verify with the binary it just downloaded ────────────────────────────────────────────

  That is circular: whoever can serve you a heros.exe can serve you one whose verify command prints "OK".
  The verifier has to predate the download. On Windows that is `ssh-keygen -Y verify`, which ships with the
  OpenSSH client included in Windows 10 1809+ and Windows 11. .NET's own crypto stack has no Ed25519 before
  .NET 8, and Windows PowerShell 5.1 runs on .NET Framework, which never will — so ssh-keygen is not a
  convenience here, it is the only verifier a stock Windows machine has.

  If it is absent, the script REFUSES and names the ways forward. It never downgrades to checksum-only:
  checksums prove the download is intact, not who produced it.

  ── The pinned trust root ───────────────────────────────────────────────────────────────────────────────

  The public key below is PINNED IN THIS SCRIPT and never downloaded — whoever can serve you a binary can
  serve you a key. It is held identical to internal/release/trustroot.go by TestInstallScriptsPinTheTrustRoot.
#>

#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo = 'damonleelcx/heros-agent'
# Overridable ONLY so the installer can be exercised against a local release fixture: a tamper red-check
# cannot be written against a script whose URLs are hard-coded, and a verify step never shown to reject is
# treated as absent. This is not a hole — the trust root below is pinned, so redirecting the download changes
# where the bytes come from, not which key must have signed them.
$Releases = if ($env:HEROS_RELEASE_BASE_URL) { $env:HEROS_RELEASE_BASE_URL } else { "https://github.com/$Repo/releases" }
$Api      = if ($env:HEROS_RELEASE_API_URL)  { $env:HEROS_RELEASE_API_URL }  else { "https://api.github.com/repos/$Repo/releases" }

$PubKeySsh    = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMniGcZXM+AMZ6J5ldw3N7P3PZp6fJHsKvbgRGNzK1YK'
$SignerId     = 'heros-release'
$SigNamespace = 'file'

function Say  { param([string]$m) Write-Host "heros install: $m" }

# Fail is the only exit path for a failure. It says what went wrong AND what to do next, because a user who
# hits a refused install has one question and it is not "what happened".
function Fail {
    param([string]$Message, [string[]]$Next = @())
    Write-Host ''
    Write-Host "heros install: [X] $Message" -ForegroundColor Red
    if ($Next.Count -gt 0) {
        Write-Host ''
        foreach ($line in $Next) { Write-Host "  $line" }
    }
    Write-Host ''
    Write-Host 'Nothing was installed. No partial install was left behind.'
    exit 1
}

# Show-Mark draws the Heros wordmark - HEROS, whose H is the mark: two end caps, the span, and a point
# estimate on it. Held to internal/distribution/mark.go by TestInstallScriptsCarryTheSameMark; edit the
# skeleton THERE, not this copy.
#
# It prints on a SUCCESSFUL install only, for the reason install.sh gives at its own copy: a logo above
# "Nothing was installed" would put the brand on a refusal.
#
# # Why the drawing is a skeleton rather than the characters it prints
#
# This script has no UTF-8 BOM, and it is normally run as `irm ... | iex` - so on Windows PowerShell 5.1 the
# bytes are decoded as the system ANSI code page, not as UTF-8. A box-drawing character typed as a literal
# here would arrive mojibake on exactly the platform this script exists to serve. So the drawing below is
# written in plain ASCII placeholders and mapped at runtime: to box-drawing code points when the console is
# genuinely in UTF-8, and to ASCII everywhere else. Digits are corner POSITIONS, never printed as digits.
#
# # Why a named console colour rather than the brand teal
#
# install.sh paints the wordmark in #2ecfa8 with a truecolor escape. A raw VT escape is not safe here:
# consoles without virtual-terminal processing print it as literal characters, and 5.1 runs on plenty of
# them. Cyan is the nearest colour the console can be asked for by name, which always renders - the same
# trade this script already makes with `[X]` for the sh script's refusal marker.
function Show-Mark {
    if ([Console]::IsOutputRedirected) { return }

    $skeleton = @(
        '##     ## ###### #####  ###### ######',
        '##     ## ##     ##  ## ##  ## ##    ',
        '##- o -## #####  #####  ##  ## ######',
        '##     ## ##     ## ##  ##  ##     ##',
        '##     ## ###### ##  ## ###### ######')

    # The two pens. Held identical to distribution.MarkGlyphs by the Go fence, so Windows cannot end up
    # drawing a different picture than everyone else.
    $box = @{ '#' = 0x2588; '-' = 0x2501; 'o' = 0x25CF }
    $ascii = @{ '#' = '#'; '-' = '-'; 'o' = 'o' }
    $utf8 = [Console]::OutputEncoding.CodePage -eq 65001

    $colour = @{}
    if (-not $env:NO_COLOR) { $colour = @{ ForegroundColor = 'Cyan' } }

    foreach ($row in $skeleton) {
        $line = ''
        foreach ($ch in $row.ToCharArray()) {
            $key = [string]$ch
            if ($ch -eq ' ') { $line += ' ' }
            elseif ($utf8 -and $box.ContainsKey($key)) { $line += [char]$box[$key] }
            elseif ($ascii.ContainsKey($key)) { $line += $ascii[$key] }
            else { $line += ' ' }
        }
        Write-Host "  $line" @colour
    }
    Write-Host ''
}

# ── uninstall (task 3.8) ────────────────────────────────────────────────────────────────────────────────
if ($env:HEROS_UNINSTALL -eq '1') {
    $found = $false
    $candidates = @()
    if ($env:HEROS_INSTALL_DIR) { $candidates += $env:HEROS_INSTALL_DIR }
    $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\heros')
    foreach ($dir in $candidates) {
        $exe = Join-Path $dir 'heros.exe'
        if (Test-Path $exe) {
            Remove-Item -Force $exe
            Say "removed $exe"
            $found = $true
            # The PATH entry is removed too. Leaving a stale PATH entry behind is how a user's shell keeps
            # reporting "heros is not recognized" from a directory that no longer holds anything.
            $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
            if ($userPath -and $userPath.Split(';') -contains $dir) {
                $kept = ($userPath.Split(';') | Where-Object { $_ -ne $dir }) -join ';'
                [Environment]::SetEnvironmentVariable('Path', $kept, 'User')
                Say "removed $dir from your user PATH"
            }
        }
    }
    if (-not $found) {
        Say 'no heros.exe found in the directories this installer writes to.'
        Say 'if you installed with a package manager, uninstall with its own command:'
        Say '  scoop uninstall heros   ·   winget uninstall HerosForeal.Heros'
    }
    exit 0
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("heros-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    # ── 1. detect the target ────────────────────────────────────────────────────────────────────────────
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        'AMD64' { $goarch = 'amd64' }
        'ARM64' {
            # A disclosed limit, not a surprise (PRD NFR5). windows/arm64 is not built: there is no native
            # arm64 Windows runner in the release matrix, and the CGO tree-sitter frontends make a cross
            # build a different, less-tested artifact. Windows' x64 emulation runs the amd64 build, so the
            # honest answer is to install that one and say why.
            Say 'windows/arm64 has no native build; installing the amd64 build, which runs under Windows x64 emulation.'
            Say 'That is a disclosed limit, not a fallback: see the support matrix in the README.'
            $goarch = 'amd64'
        }
        default {
            Fail "unsupported architecture: $arch" @(
                'heros publishes a windows/amd64 build. Adding an architecture is a new native runner in the',
                'release matrix, not a redesign - open an issue and say which one.')
        }
    }
    Say "target windows/$goarch"

    # ── 2. resolve the version ──────────────────────────────────────────────────────────────────────────
    if ($env:HEROS_VERSION) {
        $version = $env:HEROS_VERSION -replace '^v', ''
        Say "installing pinned version $version"
    } else {
        try {
            $latest = Invoke-RestMethod -Uri "$Api/latest" -UseBasicParsing -TimeoutSec 30
            $version = ($latest.tag_name) -replace '^v', ''
        } catch {
            Fail 'cannot reach the GitHub Releases API to find the latest version' @(
                'Check your network, or pin a version:',
                '  $env:HEROS_VERSION=''0.20.0''; irm .../install.ps1 | iex',
                "Releases are listed at $Releases")
        }
        if (-not $version) { Fail 'could not read a version from the Releases API response' }
        Say "latest version is $version"
    }

    $asset = "heros-$version-windows-$goarch.exe"
    $base  = "$Releases/download/v$version"

    function Get-Asset {
        param([string]$Name, [switch]$Optional)
        $dest = Join-Path $tmp $Name
        try {
            # -UseBasicParsing keeps this working on Windows PowerShell 5.1 without Internet Explorer's
            # engine, which is not present on Server Core.
            Invoke-WebRequest -Uri "$base/$Name" -OutFile $dest -UseBasicParsing -TimeoutSec 120
            return $dest
        } catch {
            if ($Optional) { return $null }
            Fail "cannot download $Name for v$version" @(
                'Either the version does not exist or your platform has no asset in it.',
                "Available releases: $Releases")
        }
    }

    Say "downloading $asset"
    $assetPath    = Get-Asset $asset
    $manifestPath = Get-Asset 'SHA256SUMS'
    $sshsigPath   = Get-Asset 'SHA256SUMS.sshsig' -Optional
    if (-not $sshsigPath) {
        Fail "release v$version carries no OpenSSH-format signature over its checksum manifest" @(
            'Checksums prove the download is intact; only the signature proves who produced it, and',
            'ssh-keygen is the only ed25519 verifier a stock Windows machine has.',
            'This installer refuses an unverifiable release. Report it with the version.')
    }

    # ── 3. checksum, then signature. The order is not arbitrary ─────────────────────────────────────────
    # The checksum proves the binary matches the manifest; the signature proves the manifest is ours.
    # Verifying the signature first would leave a window in which a trusted manifest describes a file nobody
    # has checked.
    $line = Select-String -Path $manifestPath -Pattern ([regex]::Escape($asset)) | Select-Object -First 1
    if (-not $line) {
        Fail "the release manifest does not list $asset" @(
            'This means the release is incomplete for your platform - not that your download failed.',
            "Report it with the version ($version) and platform (windows/$goarch).")
    }
    $want = ($line.Line -split '\s+')[0].ToLower()
    $got  = (Get-FileHash -Path $assetPath -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) {
        Fail "CHECKSUM MISMATCH for $asset" @(
            "manifest: $want", "download: $got", '',
            'The bytes you received are not the bytes the release published. Do not run this file.',
            'Retry once in case of a truncated download; if it repeats, report it.')
    }
    Say '[ok] checksum matches the release manifest'

    $sshKeygen = Get-Command ssh-keygen -ErrorAction SilentlyContinue
    if (-not $sshKeygen) {
        Fail 'ssh-keygen is not available, so the release signature cannot be checked' @(
            'This installer will not place an unverified binary on your PATH, and it will not use the binary',
            'it just downloaded to verify itself - whoever can serve you a binary can serve you one that',
            'prints "signature OK". .NET Framework has no Ed25519, so ssh-keygen is the only verifier a',
            'stock Windows machine has.',
            '',
            'Any one of these fixes it:',
            '  1. enable the built-in OpenSSH client (Windows 10 1809+ / Windows 11):',
            '       Add-WindowsCapability -Online -Name OpenSSH.Client~~~~0.0.1.0',
            '  2. install with a package manager, which verifies the checksum from the signed manifest:',
            '       winget install HerosForeal.Heros',
            '  3. use the container image, which needs no local install:',
            '       docker run --rm -v "${PWD}:/repo" ghcr.io/damonleelcx/heros:latest discover --repo /repo')
    }

    $signers = Join-Path $tmp 'allowed_signers'
    # ASCII, no BOM: ssh-keygen reads allowed_signers as plain text, and PowerShell 5.1's default UTF8
    # encoding writes a BOM that makes the first line unparseable.
    [System.IO.File]::WriteAllText($signers, "$SignerId namespaces=`"$SigNamespace`" $PubKeySsh`n", [System.Text.Encoding]::ASCII)

    # ssh-keygen reads the signed message from stdin. cmd's redirection is used rather than a PowerShell
    # pipeline because a PowerShell pipe re-encodes the stream as text, and the manifest must reach
    # ssh-keygen byte-for-byte or the signature will not match its own content.
    $verifyOut = & cmd /c "`"$($sshKeygen.Source)`" -Y verify -f `"$signers`" -I $SignerId -n $SigNamespace -s `"$sshsigPath`" < `"$manifestPath`" 2>&1"
    if ($LASTEXITCODE -ne 0) {
        Fail 'SIGNATURE VERIFICATION FAILED' @(
            'The checksum manifest is not signed by the heros release key pinned in this installer.',
            'Either the release was tampered with in transit, or the release key was rotated and this',
            'installer is older than the rotation.',
            '',
            "ssh-keygen said: $verifyOut",
            '',
            'Do not install this binary. Get the current installer from:',
            "  $Releases/latest")
    }
    Say '[ok] signature verified against the pinned heros release key (ssh-keygen)'

    # ── 4. install ──────────────────────────────────────────────────────────────────────────────────────
    $dest = if ($env:HEROS_INSTALL_DIR) { $env:HEROS_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\heros' }
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    $exe = Join-Path $dest 'heros.exe'

    # Windows refuses to overwrite a running executable, and a plain Copy-Item would fail with a permission
    # error that reads like a privilege problem. Renaming the old file out of the way first is the documented
    # way to replace a binary that may be in use; the rename succeeds even while the file is open.
    if (Test-Path $exe) {
        $old = "$exe.old"
        if (Test-Path $old) { Remove-Item -Force $old -ErrorAction SilentlyContinue }
        try { Move-Item -Force $exe $old } catch {
            Fail 'cannot replace the existing heros.exe' @(
                'It is probably running. Close it (or the terminal running it) and try again.')
        }
    }
    Copy-Item $assetPath $exe -Force
    Write-Host ''
    Show-Mark
    Say "[ok] installed $exe"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) { $userPath = '' }
    if ($userPath.Split(';') -notcontains $dest) {
        [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';') + ';' + $dest).TrimStart(';')), 'User')
        Say "added $dest to your user PATH - open a new terminal for it to take effect"
    }

    # ── first-run OS trust notice, printed only while the release still needs it ────────────────────────
    # SmartScreen warns about programs it has not seen signed. This build is not Authenticode-signed, so the
    # notice is printed; when signing ships, the release's attestation says so and this text goes away rather
    # than teaching users to click through warnings forever.
    Write-Host ''
    Say 'This build is not Authenticode-signed, so SmartScreen may warn the first time you run it:'
    Write-Host '    "More info" -> "Run anyway"'
    Say 'The package declares its publisher so you can confirm what you are running.'

    Write-Host ''
    Say 'next: cd into a repository and run'
    Write-Host '    heros doctor      # check this machine is ready'
    Write-Host '    heros discover    # find the agent workflow in your code'
} finally {
    if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
}
