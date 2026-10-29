# Builds astro_mosaic.exe and checks that Windows Defender lets it start.
#
# Defender's machine-learning check (Trojan:Win32/Bearfoos.A!ml, a false positive) blocks plain Go
# GUI builds at launch, even when an on-demand scan finds nothing. Two things keep it quiet:
#   - Windows resources (icon, manifest, version info) from winres/ via rsrc_windows_amd64.syso;
#   - -trimpath, which drops the C:\Users\... source paths from the binary.
# The launch check below catches a build that is still flagged, before anyone else runs it.
#
# Usage: powershell -ExecutionPolicy Bypass -File build.ps1

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$exe = Join-Path $PSScriptRoot 'astro_mosaic.exe'

Get-Process astro_mosaic -ErrorAction SilentlyContinue | Stop-Process -Force
go build -trimpath -ldflags=-H=windowsgui -o $exe .
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

$t0 = Get-Date
try {
    $p = Start-Process -FilePath $exe -PassThru
    Start-Sleep -Seconds 6
    $alive = -not $p.HasExited
    if ($alive) { Stop-Process -Id $p.Id -Force }
} catch {
    $alive = $false
}
$hit = Get-MpThreatDetection -ErrorAction SilentlyContinue |
    Where-Object { $_.InitialDetectionTime -gt $t0 -and ($_.Resources -join ' ') -like '*astro_mosaic*' }
if (-not $alive -or $hit -or -not (Test-Path $exe)) {
    throw 'Defender blocked astro_mosaic.exe at launch. Do not change Defender settings: change the build (see CLAUDE.md).'
}
"OK: $exe ($((Get-Item $exe).Length) bytes) starts and is not flagged"
