// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build darwin

package netfs_test

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-filesystems/smb"
	"github.com/go-macos/netfs"
)

// TestRealMount mounts an SMB server of this process, on 127.0.0.1 and a
// non-445 port, through the real NetFS, and round-trips a file through the
// mountpoint with plain os calls. It changes the machine's mount table, so it
// runs only with NETFS_REAL=1.
func TestRealMount(t *testing.T) {
	if os.Getenv("NETFS_REAL") != "1" {
		t.Skip("set NETFS_REAL=1 to mount a real SMB share through NetFS")
	}
	const user, password = "alice", "hunter2"

	mem := newMemFS()
	if err := mem.WriteFile("/hello.txt", []byte("from the server"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := smb.New()
	srv.AddUser(user, password)
	if err := srv.Share("share", mem); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	u := &url.URL{Scheme: "smb", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), Path: "/share"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A wrong password must fail cleanly and without a dialog. It goes first:
	// measured on macOS 27, for a few seconds after a volume of the same
	// server is unmounted, NetFS answers a mount from the session it still
	// holds and does not check the credentials it is given.
	if bad, err := netfs.Mount(ctx, u, user, "wrong", netfs.Options{AllowLoopback: true}); err == nil {
		t.Logf("unmount: %v", netfs.Unmount(bad))
		t.Fatalf("a wrong password mounted at %s", bad)
	} else {
		t.Logf("wrong password: %v (auth=%v)", err, errors.Is(err, netfs.ErrAuth))
	}

	// Loopback without AllowLoopback: the result is logged, not asserted,
	// because it is NetFS's policy and not this package's.
	if mp, err := netfs.Mount(ctx, u, user, password, netfs.Options{}); err != nil {
		t.Logf("without AllowLoopback: %v", err)
	} else {
		t.Logf("without AllowLoopback it mounted at %s; unmount: %v", mp, netfs.Unmount(mp))
	}

	mp, err := netfs.Mount(ctx, u, user, password, netfs.Options{AllowLoopback: true})
	if err != nil {
		t.Fatalf("mount %s: %v", u, err)
	}
	t.Logf("mounted %s on port %d at %s", u, port, mp)
	unmounted := false
	defer func() {
		if !unmounted {
			_ = netfs.Unmount(mp)
		}
	}()

	if !strings.HasPrefix(mp, "/Volumes/") {
		t.Errorf("mountpoint %q is not under /Volumes", mp)
	}
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if "/Volumes/"+e.Name() == mp {
			found = true
		}
	}
	if !found {
		t.Errorf("%s is not listed in /Volumes", mp)
	}

	if b, err := os.ReadFile(filepath.Join(mp, "hello.txt")); err != nil || string(b) != "from the server" {
		t.Errorf("read server file: %q, %v", b, err)
	}
	want := []byte("round trip through NetFS")
	if err := os.WriteFile(filepath.Join(mp, "back.txt"), want, 0o644); err != nil {
		t.Fatalf("write through the mount: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(mp, "back.txt"))
	if err != nil || string(got) != string(want) {
		t.Errorf("read back: %q, %v", got, err)
	}
	if b, err := mem.ReadFile("/back.txt"); err != nil || string(b) != string(want) {
		t.Errorf("server side holds %q, %v", b, err)
	}

	if err := netfs.Unmount(mp); err != nil {
		t.Fatalf("unmount: %v", err)
	}
	unmounted = true
	if _, err := os.Stat(filepath.Join(mp, "hello.txt")); err == nil {
		t.Errorf("%s still serves files after Unmount", mp)
	}
}
