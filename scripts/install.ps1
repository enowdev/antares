<#
.SYNOPSIS
  Antares installer for Windows (PowerShell).

.DESCRIPTION
  Downloads the prebuilt antares.exe for your platform from the project's GitHub
  Releases, checks it against the release's checksums.txt and installs it. No
  build tools required.

.EXAMPLE
  irm https://antares.enowx.ai/install.ps1 | iex

.NOTES
  Env knobs (set before running):
    $env:ANTARES_PREFIX    install dir; default $env:LOCALAPPDATA\Antares
    $env:ANTARES_VERSION   specific release tag; default latest
    $env:ANTARES_REPO      owner/name; default enowdev/antares
#>

$ErrorActionPreference = 'Stop'

$Repo    = if ($env:ANTARES_REPO)    { $env:ANTARES_REPO }    else { 'enowdev/antares' }
$Version = if ($env:ANTARES_VERSION) { $env:ANTARES_VERSION } else { 'latest' }
$Prefix  = if ($env:ANTARES_PREFIX)  { $env:ANTARES_PREFIX }  else { Join-Path $env:LOCALAPPDATA 'Antares' }
$BinDir  = Join-Path $Prefix 'bin'

function Info($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "warning: $m" -ForegroundColor Yellow }
# throw, not exit: under `irm | iex` exit would close the user's window.
function Die($m)  { throw "error: $m" }
function Have($c) { [bool](Get-Command $c -ErrorAction SilentlyContinue) }

# ---- detect arch ------------------------------------------------------------
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
  'AMD64' { $goarch = 'amd64' }
  'ARM64' { $goarch = 'arm64' }
  default { Die "unsupported architecture '$arch'" }
}
Info "platform: windows/$goarch"

function AssetFor($ver) { "antares_${ver}_windows_${goarch}.exe" }

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$dest = Join-Path $BinDir 'antares.exe'
$tmp  = Join-Path ([System.IO.Path]::GetTempPath()) ("antares-" + [System.Guid]::NewGuid().ToString('N') + '.exe')

# ---- fetch ------------------------------------------------------------------
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$ProgressPreference = 'SilentlyContinue'  # the progress bar makes downloads crawl on 5.1
$ver = $Version
# /releases/latest redirects to /releases/tag/<tag>: no API, so no rate limit.
# The API is only a fallback.
function LatestTag {
  try {
    $req = [System.Net.HttpWebRequest]::Create("https://github.com/$Repo/releases/latest")
    $req.AllowAutoRedirect = $false
    $req.Method = 'HEAD'
    $req.UserAgent = 'antares-installer'
    $resp = $req.GetResponse()
    $loc = $resp.Headers['Location']
    $resp.Close()
    if ($loc -match '/tag/(v[^/]+)$') { return $Matches[1] }
  } catch {}
  try { return (Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest").tag_name } catch { return $null }
}
if ($ver -eq 'latest') {
  $ver = LatestTag
  if (-not $ver) { Die "could not find the latest release of $Repo." }
}
$base  = "https://github.com/$Repo/releases/download/$ver"
$asset = AssetFor $ver
Info "downloading $asset ($ver)"
try { Invoke-WebRequest -Uri "$base/$asset" -OutFile $tmp -UseBasicParsing }
catch { Die "download failed: $base/$asset" }

if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -eq 0) { Die "downloaded file is empty." }

# ---- verify -----------------------------------------------------------------
try {
  $sums = (Invoke-WebRequest -Uri "$base/checksums.txt" -UseBasicParsing).Content
  if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
  $want = $null
  foreach ($line in ($sums -split "`n")) {
    $parts = $line.Trim() -split '\s+'
    if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $asset) { $want = $parts[0].ToLower() }
  }
  $got = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLower()
  if (-not $want) { Warn "no checksum listed for $asset; continuing." }
  elseif ($want -ne $got) { Remove-Item -Force $tmp; Die "checksum mismatch for $asset (expected $want, got $got)." }
  else { Info "checksum ok" }
} catch {
  Warn "could not fetch checksums.txt; skipping verification."
}

# ---- install ----------------------------------------------------------------
Move-Item -Force $tmp $dest
Info "installed $dest"
try { & $dest --version } catch {}
# An older release does not know --version; its exit code is not the install's.
$global:LASTEXITCODE = 0

# ---- PATH -------------------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path','User')
if ($userPath -notlike "*$BinDir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$BinDir", 'User')
  Warn "added $BinDir to your user PATH - open a NEW terminal for it to take effect."
} else {
  Info "run 'antares' from anywhere (open a new terminal if not found yet)"
}

Write-Host ""
Info "next: run 'antares setup' to configure a provider, then 'antares' to start it in the background (or 'antares tui' for the terminal UI)"
