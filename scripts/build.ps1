# Builds release binaries for Windows and Linux into dist/.
# Usage: powershell -ExecutionPolicy Bypass -File scripts\build.ps1 [-Version v1.0.0]
param(
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

if ($Version -eq "") {
    $Version = (git describe --tags --always 2>$null)
    if (-not $Version) { $Version = "dev" }
}

$ldflags = "-s -w -X main.version=$Version"
New-Item -ItemType Directory -Force dist | Out-Null

$targets = @(
    @{ GOOS = "windows"; GOARCH = "amd64"; Out = "mc-tui-server_windows_amd64.exe" },
    @{ GOOS = "linux";   GOARCH = "amd64"; Out = "mc-tui-server_linux_amd64" },
    @{ GOOS = "linux";   GOARCH = "arm64"; Out = "mc-tui-server_linux_arm64" }
)

foreach ($t in $targets) {
    Write-Host "Building $($t.Out) ($Version)..."
    $env:GOOS = $t.GOOS
    $env:GOARCH = $t.GOARCH
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags $ldflags -o (Join-Path "dist" $t.Out) .
    if ($LASTEXITCODE -ne 0) { throw "build failed for $($t.Out)" }
}

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
Write-Host "Done. Binaries in dist/:"
Get-ChildItem dist | Format-Table Name, Length
