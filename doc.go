// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

// Package netfs mounts an SMB or WebDAV URL on macOS through Apple's NetFS
// framework (NetFSMountURLSync) and unmounts it again, from pure Go with
// CGO_ENABLED=0.
//
// The user name and password are handed to NetFS as CFStrings. They never
// appear in an argument vector (as with mount_smbfs) or in the environment, and
// the open options carry UIOption=NoUI so that no dialog can appear: the call
// either mounts or fails.
//
// The volume is mounted by the system, so it appears in /Volumes and, like any
// network volume, in Finder.
//
//	u, _ := url.Parse("smb://127.0.0.1:4445/share")
//	mp, err := netfs.Mount(ctx, u, "alice", "hunter2", netfs.Options{AllowLoopback: true})
//	if err != nil { ... }
//	defer netfs.Unmount(mp)
//
// Every symbol exists on every platform; off darwin the operations return
// [ErrUnsupported].
package netfs
