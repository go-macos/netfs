# go-macos/netfs

[![ci](https://github.com/go-macos/netfs/actions/workflows/ci.yml/badge.svg)](https://github.com/go-macos/netfs/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-macos/netfs.svg)](https://pkg.go.dev/github.com/go-macos/netfs)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

**Mount an SMB or WebDAV URL on macOS from pure Go (`CGO_ENABLED=0`) through
Apple's NetFS framework, and unmount it again.** The user name and password go
to `NetFSMountURLSync` as CFStrings: never in an argument vector (`mount_smbfs`
shows them to `ps`), never in the environment, never in the URL. NetFS is told
`UIOption=NoUI`, so no dialog can appear. It reaches the framework through
[purego](https://github.com/ebitengine/purego) and
[`go-macos/objc`](https://github.com/go-macos/objc).

```go
u, _ := url.Parse("smb://127.0.0.1:4445/share")
mp, err := netfs.Mount(ctx, u, "alice", "hunter2", netfs.Options{AllowLoopback: true})
if err != nil {
	return err // errors.Is(err, netfs.ErrAuth), syscall.ECONNREFUSED, ...
}
defer netfs.Unmount(mp)
// mp == "/Volumes/share": plain os.ReadFile / os.WriteFile work on it.
```

## API

| | |
|---|---|
| `Mount(ctx, *url.URL, user, password, Options) (mountpoint, error)` | `NetFSMountURLSync`; returns the first mountpoint. |
| `Unmount(mountpoint) error` | `unmount(2)`; `EBUSY` while a file is open. |
| `Options` | `MountDir`, `ReadOnly`, `Hidden` (`MNT_DONTBROWSE`), `Guest`, `AllowLoopback`, `AllowSubMounts`. |
| `*Error`, `ErrAuth`, `ErrCanceled`, ... | NetFS's return value is an errno when positive and an OSStatus when negative; both are mapped, and `errors.Is` sees the sentinel or the `syscall.Errno`. |

The URL must be `smb://`, `http://` or `https://` (WebDAV) with a host, and
must carry no user information: credentials are arguments, so they cannot end
up in a log line or in the volume's mounted-from name.

The volume is mounted by the system under `/Volumes`, like any network volume;
leave `Hidden` false for Finder to list it.

If `ctx` ends before NetFS returns, `Mount` returns `ctx.Err()` and a mount
that completes afterwards is unmounted again, so cancellation never leaves a
volume behind.

## Measured on macOS 27 (arm64)

- A server on `127.0.0.1` at a **non-445 port** mounts: `smb://127.0.0.1:<port>/share`.
  NetFS accepts the port in the URL.
- 127.0.0.1 also mounted **without** `AllowLoopback`; the option is
  documented in `NetFS.h` ("Allow a loopback mount"), so set it for a local
  server anyway.
- A wrong password fails with `EAUTH` (80, `ErrAuth`) and no dialog.
- **Credential caching**: for a few seconds after a volume of the same server
  is unmounted, a mount with a *wrong* password succeeded, because NetFS served
  it from the session it still held. Do not use `Mount` as a password check
  right after an `Unmount` of the same server.

## Every platform

macOS only, but every symbol exists everywhere and `Mount` / `Unmount` return
`ErrUnsupported` off darwin, so a consumer cross-compiles without build tags.

## Tests

Coverage is **100 %**, statement for statement, on darwin and off it; every
bound C entry point is a package variable so failure branches are reached by
replacing the call. `NETFS_REAL=1 go test` also starts an SMB server of this
process ([`go-filesystems/smb`](https://github.com/go-filesystems/smb)), mounts
it through `Mount`, writes and reads a file through the mountpoint with plain
`os` calls, and unmounts.

## Install

```
go get github.com/go-macos/netfs
```

BSD-3-Clause.
