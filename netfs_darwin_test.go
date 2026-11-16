// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build darwin

package netfs

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/go-macos/objc"
)

// cfArrayOf builds a CFArray of CFStrings with CFArrayCreate (retained, as
// NetFS returns it).
func cfArrayOf(t *testing.T, items ...string) uintptr {
	t.Helper()
	h, err := purego.Dlopen(objc.CoreFoundation, purego.RTLD_NOW)
	if err != nil {
		t.Fatal(err)
	}
	p, err := purego.Dlsym(h, "CFArrayCreate")
	if err != nil {
		t.Fatal(err)
	}
	var create func(alloc uintptr, values *uintptr, n int, cb uintptr) uintptr
	purego.RegisterFunc(&create, p)
	cb, err := purego.Dlsym(h, "kCFTypeArrayCallBacks")
	if err != nil {
		t.Fatal(err)
	}
	vals := make([]uintptr, len(items)+1) // +1: never an empty slice
	for i, s := range items {
		vals[i] = cfString(s)
	}
	arr := create(0, &vals[0], len(items), cb)
	for i := range items {
		cfRelease(vals[i])
	}
	return arr
}

func TestLoaded(t *testing.T) {
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfTrue == 0 || cfFalse == 0 || cfTrue == cfFalse {
		t.Errorf("CFBoolean values %#x %#x", cfTrue, cfFalse)
	}
}

func TestBindAllFailures(t *testing.T) {
	if err := bindAll("/nonexistent/CoreFoundation", Framework); err == nil || !strings.Contains(err.Error(), "load CoreFoundation") {
		t.Errorf("bad CoreFoundation path: %v", err)
	}
	if err := bindAll(objc.CoreFoundation, "/nonexistent/NetFS"); err == nil || !strings.Contains(err.Error(), "load NetFS") {
		t.Errorf("bad NetFS path: %v", err)
	}
	// A library that exists and exports none of NetFS's symbols.
	if err := bindAll(objc.CoreFoundation, "/usr/lib/libSystem.B.dylib"); err == nil || !strings.Contains(err.Error(), "missing symbol NetFSMountURLSync") {
		t.Errorf("library without NetFS symbols: %v", err)
	}
	// The failing calls left the real bindings working.
	if err := bindAll(objc.CoreFoundation, Framework); err != nil {
		t.Errorf("rebinding: %v", err)
	}
}

func TestStringsAndArrays(t *testing.T) {
	s := cfString("smb://héllo/share")
	defer cfRelease(s)
	if got := goString(s); got != "smb://héllo/share" {
		t.Errorf("goString %q", got)
	}
	arr := cfArrayOf(t, "/Volumes/a", "/Volumes/é")
	defer cfRelease(arr)
	got := arrayStrings(arr)
	if len(got) != 2 || got[0] != "/Volumes/a" || got[1] != "/Volumes/é" {
		t.Errorf("arrayStrings %q", got)
	}
	empty := cfArrayOf(t)
	defer cfRelease(empty)
	if got := arrayStrings(empty); len(got) != 0 {
		t.Errorf("empty array: %q", got)
	}
}

func TestNewDict(t *testing.T) {
	d, err := newDict(map[string]any{"s": "v", "b": true, "f": false, "n": int32(7)})
	if err != nil || d == 0 {
		t.Fatalf("%v", err)
	}
	cfRelease(d)
	if _, err := newDict(map[string]any{"bad": 1.5}); err == nil || !strings.Contains(err.Error(), `"bad"`) {
		t.Errorf("unsupported type: %v", err)
	}
}

// TestRealMountSyncCallFails makes the real NetFSMountURLSync call against a
// port nobody listens on, so no volume can appear.
func TestRealMountSyncCallFails(t *testing.T) {
	u, _ := url.Parse("smb://127.0.0.1:1/none")
	_, err := Mount(context.Background(), u, "nobody", "nothing", Options{AllowLoopback: true, ReadOnly: true, Hidden: true, MountDir: "/Volumes"})
	var e *Error
	if !errors.As(err, &e) || e.Status == 0 {
		t.Fatalf("expected a NetFS error, got %v", err)
	}
	t.Logf("closed port: %v", err)
}

func TestRealMountReturnsAndReleasesArray(t *testing.T) {
	orig := netfsMountURLSync
	t.Cleanup(func() { netfsMountURLSync = orig })
	var gotUser, gotPass, gotDir uintptr
	netfsMountURLSync = func(url, mountpath, user, passwd, openOpts, mountOpts uintptr, points *uintptr) int32 {
		gotUser, gotPass, gotDir = user, passwd, mountpath
		*points = cfArrayOf(t, "/Volumes/fake")
		return 0
	}
	got, status, err := realMount(request{url: "smb://h/s", user: "u", password: "p", mountDir: "/Volumes", open: map[string]any{}, mount: map[string]any{}})
	if err != nil || status != 0 || len(got) != 1 || got[0] != "/Volumes/fake" {
		t.Fatalf("%q %d %v", got, status, err)
	}
	if gotUser == 0 || gotPass == 0 || gotDir == 0 {
		t.Error("user, password or mount directory not passed")
	}
	netfsMountURLSync = func(url, mountpath, user, passwd, openOpts, mountOpts uintptr, points *uintptr) int32 {
		gotUser, gotPass, gotDir = user, passwd, mountpath
		return int32(syscall.ENOENT)
	}
	got, status, err = realMount(request{url: "smb://h/s", open: map[string]any{}, mount: map[string]any{}})
	if err != nil || status != int32(syscall.ENOENT) || got != nil {
		t.Errorf("%q %d %v", got, status, err)
	}
	if gotUser != 0 || gotPass != 0 || gotDir != 0 {
		t.Error("empty user, password and directory must be NULL")
	}
}

func TestRealMountLocalFailures(t *testing.T) {
	good := map[string]any{}
	if _, _, err := realMount(request{url: "smb://[bad", open: good, mount: good}); !errors.Is(err, ErrBadURL) {
		t.Errorf("CFURL refusal: %v", err)
	}
	bad := map[string]any{"x": 1.5}
	if _, _, err := realMount(request{url: "smb://h/s", open: bad, mount: good}); err == nil {
		t.Error("bad open options accepted")
	}
	if _, _, err := realMount(request{url: "smb://h/s", open: good, mount: bad}); err == nil {
		t.Error("bad mount options accepted")
	}
	saved := loadErr
	loadErr = errors.New("not loaded")
	defer func() { loadErr = saved }()
	if _, _, err := realMount(request{url: "smb://h/s"}); err == nil || err.Error() != "not loaded" {
		t.Errorf("load failure: %v", err)
	}
}

func TestRealUnmount(t *testing.T) {
	// Not a mountpoint: the kernel refuses, and nothing is unmounted.
	err := Unmount(t.TempDir())
	if !errors.Is(err, syscall.EINVAL) {
		t.Errorf("unmounting a plain directory: %v", err)
	}
}

func TestDerefReadsThroughTheAddress(t *testing.T) {
	v := uintptr(0xdecafbad)
	if got := deref(uintptr(unsafe.Pointer(&v))); got != v {
		t.Errorf("deref %#x", got)
	}
}
