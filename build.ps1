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
$t0 = Get-Date

Get-Process astro_mosaic -ErrorAction SilentlyContinue | Stop-Process -Force
go build -trimpath -ldflags=-H=windowsgui -o $exe .
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

try {
    $p = Start-Process -FilePath $exe -PassThru
    # Defender can remediate a GUI build after the first few seconds of startup.
    for ($second = 0; $second -lt 30 -and -not $p.HasExited; $second++) {
        Start-Sleep -Seconds 1
        $p.Refresh()
    }
    $alive = -not $p.HasExited
    if ($alive) { Stop-Process -Id $p.Id -Force }
} catch {
    $alive = $false
}
$hit = Get-MpThreatDetection -ErrorAction SilentlyContinue |
    Where-Object {
        ($_.InitialDetectionTime -ge $t0 -or $_.LastThreatStatusChangeTime -ge $t0) -and
        ($_.Resources -join ' ') -like '*astro_mosaic*'
    }
if (-not $alive -or $hit -or -not (Test-Path $exe)) {
    throw 'astro_mosaic.exe failed the launch/Defender check. Do not change Defender settings: change the build (see AGENTS.md).'
}
"OK: $exe ($((Get-Item $exe).Length) bytes) stays running for 30 seconds and is not flagged"
