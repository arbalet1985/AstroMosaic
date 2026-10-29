# AstroMosaic

Go + Gio stitcher for astro mosaics (Windows). Repo: https://github.com/arbalet1985/AstroMosaic, branch `main`.

## Building the exe — always via build.ps1

```
powershell -ExecutionPolicy Bypass -File build.ps1
```

Never hand over, release or tell the user to run an exe built any other way.

Windows Defender flags Go GUI builds as `Trojan:Win32/Bearfoos.A!ml`. It is a machine-learning false positive, and it hit
on 2026-10-27 and again on 2026-10-29. Defender deletes the exe or blocks it when it starts. What keeps it quiet:

- **`-trimpath`** is required. Builds without it, i.e. `go build -ldflags=-H=windowsgui`, were blocked at launch. Builds
  with it passed on four different commits.
- **`rsrc_windows_amd64.syso`** must be kept. It holds the icon, manifest and version info from `winres/`, and
  `go build` picks it up on its own. Re-run `go-winres make` after editing `winres/winres.json`.
- **Do not add `-s -w`.** Stripped builds were deleted as soon as they were written.

An on-demand scan, `MpCmdRun -Scan -ScanType 3 -File <exe>`, is **not** proof: it said "no threats" for a build that
Defender then blocked at launch. Only a real launch tells. `build.ps1` does that launch and fails if the exe does not
start or a new detection names it. Check detections with `Get-MpThreatDetection`.

If the check fails, change the build, not Defender. Try bumping the version in `winres/winres.json` and re-running
`go-winres make`, or another harmless build variation, then run the launch check again. Never change Defender settings
or add exclusions.

## Tests

`go test .` runs everything. Tests that need the real tiles skip themselves when the files are absent:
`realgrid_test.go`, and the xisf tiles in `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\` (1-1, 1-2, 2_1, 2_2).
The names of those tiles do not match their positions on the sky; the names are wrong on purpose.

## Releases

GitHub releases are made through the API, using the token from `git credential fill`; `gh` is not installed.
Uploading assets can be reset by the network, so use `curl --http1.1` and retry.

## Editing

When rewriting `.go` files from Python, open them with `newline=''`. Otherwise Windows writes CRLF and `gofmt -l`
lists the files.
