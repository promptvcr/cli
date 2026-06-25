#Requires -Version 5
<#
.SYNOPSIS
  PromptVCR CLI installer for Windows.

.DESCRIPTION
  Detects the architecture, downloads the matching release archive from
  promptvcr/cli, verifies it against checksums.txt, and installs promptvcr.exe.

  Run:
    irm https://raw.githubusercontent.com/promptvcr/cli/main/scripts/install.ps1 | iex

.NOTES
  Env overrides:
    PROMPTVCR_VERSION       pin a version (e.g. v0.2.0); default: latest release
    PROMPTVCR_INSTALL_DIR   install directory; default: $env:LOCALAPPDATA\promptvcr\bin
#>
$ErrorActionPreference = "Stop"

$repo = "promptvcr/cli"
$binary = "promptvcr.exe"
$installDir = if ($env:PROMPTVCR_INSTALL_DIR) { $env:PROMPTVCR_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "promptvcr\bin" }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'" }
}

$tag = $env:PROMPTVCR_VERSION
if (-not $tag) {
  $latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ "User-Agent" = "promptvcr-install" }
  $tag = $latest.tag_name
}
if (-not $tag) { throw "could not resolve a release tag (set PROMPTVCR_VERSION)" }
$version = $tag.TrimStart("v")

$archive = "promptvcr_${version}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/$tag"
$tmp = Join-Path $env:TEMP ("promptvcr-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

try {
  Write-Host "Downloading promptvcr $tag (windows/$arch)..."
  Invoke-WebRequest -Uri "$base/$archive" -OutFile (Join-Path $tmp $archive) -UseBasicParsing

  try {
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt") -UseBasicParsing
    $line = (Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match [regex]::Escape($archive) + '$' } | Select-Object -First 1)
    if ($line) {
      $expected = ($line -split '\s+')[0]
      $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
      if ($actual -ne $expected.ToLower()) { throw "checksum mismatch for $archive" }
      Write-Host "Checksum verified."
    }
  } catch {
    Write-Warning "checksums.txt not found or unreadable; skipping verification"
  }

  Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force
  $src = Join-Path $tmp $binary
  if (-not (Test-Path $src)) { throw "archive did not contain '$binary'" }

  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  Copy-Item -Force $src (Join-Path $installDir $binary)

  Write-Host ""
  Write-Host "Installed promptvcr to $installDir\$binary"

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
    Write-Host "Added $installDir to your user PATH (restart your shell to pick it up)."
  }
  Write-Host "Next: promptvcr init; promptvcr doctor"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
