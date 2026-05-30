# Install the Emailable CLI on Windows.
#
# Usage (PowerShell):
#   irm https://emailable.com/install-cli.ps1 | iex
#
# Environment overrides:
#   $env:EMAILABLE_VERSION   Specific version to install (e.g. v0.2.0).
#                            Defaults to the latest GitHub release.
#   $env:EMAILABLE_PREFIX    Install prefix. Defaults to
#                            "$env:LOCALAPPDATA\Programs\emailable". The
#                            binary lands directly under this directory.

$ErrorActionPreference = 'Stop'

$Repo   = 'emailable/emailable-cli'
$Binary = 'emailable'

function Abort([string]$msg) {
  Write-Host "Error: $msg" -ForegroundColor Red
  exit 1
}

# --- detect architecture --------------------------------------------------

# A 32-bit PowerShell on 64-bit Windows reports x86 in PROCESSOR_ARCHITECTURE;
# PROCESSOR_ARCHITEW6432 holds the true machine arch in that case.
$procArch = $env:PROCESSOR_ARCHITEW6432
if (-not $procArch) { $procArch = $env:PROCESSOR_ARCHITECTURE }

$arch = switch ($procArch) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { Abort "unsupported architecture: $procArch" }
}

# --- resolve version ------------------------------------------------------

$version = $env:EMAILABLE_VERSION
if (-not $version) {
  try {
    $resp = Invoke-WebRequest -UseBasicParsing -MaximumRedirection 0 `
      -Uri "https://github.com/$Repo/releases/latest" -ErrorAction SilentlyContinue
  } catch {
    $resp = $_.Exception.Response
  }
  $location = $null
  if ($resp -and $resp.Headers) { $location = $resp.Headers['Location'] }
  if (-not $location) { Abort "could not determine latest version" }
  $version = ($location -split '/tag/')[-1]
}
$version = $version.TrimStart('v')
$tag     = "v$version"

# --- pick prefix ----------------------------------------------------------

$prefix = $env:EMAILABLE_PREFIX
if (-not $prefix) {
  $prefix = Join-Path $env:LOCALAPPDATA 'Programs\emailable'
}
New-Item -ItemType Directory -Force -Path $prefix | Out-Null

# --- download & verify ----------------------------------------------------

$archive  = "${Binary}_${version}_windows_${arch}.zip"
$baseUrl  = "https://github.com/$Repo/releases/download/$tag"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  Write-Host "Downloading $archive from $tag..."
  Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$archive"        -OutFile (Join-Path $tmp $archive)
  Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/checksums.txt"   -OutFile (Join-Path $tmp 'checksums.txt')

  Write-Host "Verifying checksum..."
  $expected = (Get-Content (Join-Path $tmp 'checksums.txt') `
                 | Where-Object { $_ -match "  $([regex]::Escape($archive))$" } `
                 | ForEach-Object { ($_ -split '\s+')[0] } `
                 | Select-Object -First 1)
  if (-not $expected) { Abort "no checksum entry for $archive" }

  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
  if ($expected.ToLower() -ne $actual) {
    Abort "checksum mismatch (expected $expected, got $actual)"
  }

  Write-Host "Installing to $prefix\$Binary.exe..."
  Expand-Archive -Force -LiteralPath (Join-Path $tmp $archive) -DestinationPath $tmp
  Copy-Item -Force -Path (Join-Path $tmp "$Binary.exe") -Destination (Join-Path $prefix "$Binary.exe")
} finally {
  Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}

# --- ensure prefix is on the user PATH ------------------------------------

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
$pathParts = $userPath -split ';' | Where-Object { $_ -ne '' }
if (-not ($pathParts -contains $prefix)) {
  $newPath = if ($userPath) { "$userPath;$prefix" } else { $prefix }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  Write-Host "Added $prefix to your user PATH. Open a new terminal to pick up the change." -ForegroundColor Yellow
}

Write-Host "Installed $Binary $version to $prefix\$Binary.exe" -ForegroundColor Green
