# network-utility one-line installer (Windows, PowerShell).
#
#   irm https://raw.githubusercontent.com/zanlah/network-utility/main/install.ps1 | iex
#
# To pass installer flags, fetch then invoke as a scriptblock (piping to iex can't
# take arguments):
#
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/zanlah/network-utility/main/install.ps1))) --apps ports -y
#
# What it does: fetches (or updates) the repo into %LOCALAPPDATA%\network-utility\src,
# then runs the existing Go installer from there. Re-running pulls the latest source
# first, so there's nothing to download or `git pull` by hand.
$ErrorActionPreference = 'Stop'

$Repo = 'https://github.com/zanlah/network-utility.git'
$Zip  = 'https://github.com/zanlah/network-utility/archive/refs/heads/main.zip'
$Src  = if ($env:NETUTIL_SRC) { $env:NETUTIL_SRC } else { Join-Path $env:LOCALAPPDATA 'network-utility\src' }

Write-Host 'network-utility installer'

# 1. Go is the one hard prerequisite — the installer builds the tray tools from source.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error 'Go is not installed or not on PATH. Install Go 1.21+ from https://go.dev/dl and re-run this.'
    exit 1
}

# 2. Fetch (or update) the source. Prefer git so re-runs are a fast fast-forward;
#    fall back to the GitHub zip when git isn't available.
if (Get-Command git -ErrorAction SilentlyContinue) {
    if (Test-Path (Join-Path $Src '.git')) {
        Write-Host "updating source in $Src…"
        git -C $Src pull --ff-only
    } else {
        Write-Host "cloning into $Src…"
        if (Test-Path $Src) { Remove-Item -Recurse -Force $Src }
        git clone --depth 1 $Repo $Src
    }
} else {
    Write-Host "git not found — downloading zip into $Src…"
    $tmp = Join-Path $env:TEMP 'network-utility.zip'
    Invoke-WebRequest -Uri $Zip -OutFile $tmp
    if (Test-Path $Src) { Remove-Item -Recurse -Force $Src }
    $parent = Split-Path $Src -Parent
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    Expand-Archive -Path $tmp -DestinationPath $parent -Force
    Rename-Item (Join-Path $parent 'network-utility-main') (Split-Path $Src -Leaf)
    Remove-Item $tmp
}

# 3. Hand off to the real installer, passing through any flags.
Set-Location $Src
go run ./installer install @args
