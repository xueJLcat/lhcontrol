# Patched TinyGo Bluetooth module

This directory is a Windows-only source subset of
[`tinygo.org/x/bluetooth` v0.15.0](https://github.com/tinygo-org/bluetooth/releases/tag/v0.15.0).
The root `go.mod` uses a local `replace` directive so lhcontrol can carry
Windows WinRT fixes that are not yet available in the upstream release.

## Local patches

The intentional changes from v0.15.0 are limited to:

- `adapter_windows.go`: validate WinRT adapter results and release COM objects safely.
- `gap_windows.go`: serialize watcher start/stop, make completion single-shot, and
  guard callbacks and teardown against nil pointers, duplicate release, and panic;
  expose actionable WinRT radio-unavailable, resource-in-use, and policy errors.
- `gattc_windows.go`: validate GATT/WinRT results and consistently release temporary
  COM objects.

The remaining files are the minimum upstream sources required to compile the
Windows package. Linux, macOS, TinyGo boards, examples, generators, and upstream
tests are intentionally not vendored. This module is therefore not a
cross-platform replacement for the upstream project.

## Updating

1. Download the exact upstream release to a temporary directory.
2. Copy the Windows package subset while preserving this README and the three
   patched files.
3. Reapply and review the Windows patches against the new upstream
   implementations; do not copy them blindly across versions.
4. Update the version in this README, `version.go`, the root `go.mod`, and this
   module's `go.mod`.
5. On Windows, run `go test ./...` in this directory, then run the root test,
   race, vet, frontend, and Wails Windows build checks.
6. Complete the real-hardware regression checklist in the root README before
   publishing a release.

The root lifecycle tests exercise cancellation and 100 repeated scan cycles
through the adapter interface. They do not replace real WinRT and Lighthouse
hardware testing.
