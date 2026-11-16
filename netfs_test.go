// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package netfs

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// swap replaces both seams for one test and restores them.
func swap(t *testing.T, m func(request) ([]string, int32, error), u func(string) error) {
	t.Helper()
	om, ou := mountFn, unmountFn
	t.Cleanup(func() { mountFn, unmountFn = om, ou })
	if m != nil {
		mountFn = m
	}
	if u != nil {
		unmountFn = u
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestOptionKeysMatchNetFSHeader(t *testing.T) {
	// NetFS.h spells these; a typo is silent in C.
	for got, want := range map[string]string{
		keyUIOption: "UIOption", valueNoUI: "NoUI", keyGuest: "Guest",
		keyAllowLoopback: "AllowLoopback", keyMountFlags: "MountFlags",
		keyAllowSubMounts: "AllowSubMounts",
	} {
		if got != want {
			t.Errorf("key %q, header says %q", got, want)
		}
	}
	if mntRdonly != 1 || mntDontBrowse != 0x00100000 {
		t.Errorf("MNT flags %#x %#x", mntRdonly, mntDontBrowse)
	}
}

func TestBuildOptions(t *testing.T) {
	if got, want := buildOpenOptions(Options{}), (map[string]any{"UIOption": "NoUI"}); !reflect.DeepEqual(got, want) {
		t.Errorf("zero open options %v", got)
	}
	all := Options{Guest: true, AllowLoopback: true, ReadOnly: true, Hidden: true, AllowSubMounts: true}
	if got, want := buildOpenOptions(all), (map[string]any{"UIOption": "NoUI", "Guest": true, "AllowLoopback": true}); !reflect.DeepEqual(got, want) {
		t.Errorf("open options %v", got)
	}
	if got := buildMountOptions(Options{}); len(got) != 0 {
		t.Errorf("zero mount options %v", got)
	}
	if got, want := buildMountOptions(all), (map[string]any{"MountFlags": int32(0x00100001), "AllowSubMounts": true}); !reflect.DeepEqual(got, want) {
		t.Errorf("mount options %v", got)
	}
	if got := buildMountOptions(Options{ReadOnly: true})["MountFlags"]; got != int32(1) {
		t.Errorf("read-only flags %v", got)
	}
	if got := buildMountOptions(Options{Hidden: true})["MountFlags"]; got != int32(0x00100000) {
		t.Errorf("hidden flags %v", got)
	}
}

func TestCheckURL(t *testing.T) {
	for _, s := range []string{"smb://host/share", "SMB://host:4445/share", "http://host/dav", "https://host:8443/dav"} {
		got, err := checkURL(mustURL(t, s))
		if err != nil || got != mustURL(t, s).String() {
			t.Errorf("%s: %q, %v", s, got, err)
		}
	}
	for s, want := range map[string]error{
		"ftp://host/x":        ErrBadURL,
		"smb:///share":        ErrBadURL,
		"/just/a/path":        ErrBadURL,
		"smb://u:p@host/s":    ErrCredentialsInURL,
		"https://user@host/x": ErrCredentialsInURL,
	} {
		if _, err := checkURL(mustURL(t, s)); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", s, err, want)
		}
	}
	if _, err := checkURL(nil); !errors.Is(err, ErrBadURL) {
		t.Errorf("nil URL: %v", err)
	}
}

func TestNewRequest(t *testing.T) {
	u := mustURL(t, "smb://127.0.0.1:4445/share")
	r, err := newRequest(u, "alice", "pw", Options{MountDir: "/Volumes", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.url != u.String() || r.user != "alice" || r.password != "pw" || r.mountDir != "/Volumes" ||
		r.open["UIOption"] != "NoUI" || r.mount["MountFlags"] != int32(1) {
		t.Errorf("request %+v", r)
	}
	if _, err := newRequest(nil, "", "", Options{}); !errors.Is(err, ErrBadURL) {
		t.Errorf("nil URL: %v", err)
	}
	if _, err := newRequest(u, "", "pw", Options{}); !errors.Is(err, ErrPasswordWithoutUser) {
		t.Errorf("password without user: %v", err)
	}
	if _, err := newRequest(u, "a", "b", Options{MountDir: "relative"}); !errors.Is(err, ErrBadMountDir) {
		t.Errorf("relative MountDir: %v", err)
	}
	if _, err := newRequest(u, "", "", Options{Guest: true}); err != nil {
		t.Errorf("guest without user: %v", err)
	}
}

func TestMount(t *testing.T) {
	u := mustURL(t, "smb://127.0.0.1:4445/share")
	var seen request
	swap(t, func(r request) ([]string, int32, error) {
		seen = r
		return []string{"/Volumes/share", "/Volumes/other"}, 0, nil
	}, nil)
	mp, err := Mount(context.Background(), u, "alice", "pw", Options{AllowLoopback: true})
	if err != nil || mp != "/Volumes/share" {
		t.Fatalf("%q, %v", mp, err)
	}
	if seen.user != "alice" || seen.password != "pw" || seen.open["AllowLoopback"] != true {
		t.Errorf("request %+v", seen)
	}
	// The password is not in the URL string NetFS is given.
	if strings.Contains(seen.url, "pw") || strings.Contains(seen.url, "alice") {
		t.Errorf("credentials leaked into the URL %q", seen.url)
	}
}

func TestMountFailures(t *testing.T) {
	u := mustURL(t, "smb://host/share")
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		points []string
		status int32
		err    error
		want   error
	}{
		"call failed":    {err: boom, want: boom},
		"auth":           {status: 80, want: ErrAuth},
		"errno":          {status: int32(syscall.ENOENT), want: syscall.ENOENT},
		"canceled":       {status: -128, want: ErrCanceled},
		"no mountpoints": {want: ErrNoMountpoint},
	} {
		swap(t, func(request) ([]string, int32, error) { return tc.points, tc.status, tc.err }, nil)
		if _, err := Mount(context.Background(), u, "a", "b", Options{}); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	var e *Error
	swap(t, func(request) ([]string, int32, error) { return nil, -5997, nil }, nil)
	_, err := Mount(context.Background(), u, "a", "b", Options{})
	if !errors.As(err, &e) || e.Op != "mount" || e.Status != -5997 {
		t.Errorf("errors.As: %v", err)
	}
}

func TestMountBadArguments(t *testing.T) {
	called := false
	swap(t, func(request) ([]string, int32, error) { called = true; return nil, 0, nil }, nil)
	if _, err := Mount(context.Background(), nil, "", "", Options{}); !errors.Is(err, ErrBadURL) {
		t.Errorf("nil URL: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Mount(ctx, mustURL(t, "smb://h/s"), "", "", Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: %v", err)
	}
	if called {
		t.Error("NetFS was called for invalid arguments")
	}
}

func TestMountContextCanceledUnmountsLateMount(t *testing.T) {
	release := make(chan struct{})
	unmounted := make(chan string, 1)
	swap(t, func(request) ([]string, int32, error) {
		<-release
		return []string{"/Volumes/late"}, 0, nil
	}, func(p string) error { unmounted <- p; return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := Mount(ctx, mustURL(t, "smb://h/s"), "", "", Options{Guest: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	close(release)
	select {
	case p := <-unmounted:
		if p != "/Volumes/late" {
			t.Errorf("unmounted %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a mount that completed after cancellation was left mounted")
	}
}

func TestMountContextCanceledLateFailureUnmountsNothing(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	swap(t, func(request) ([]string, int32, error) {
		<-release
		defer close(finished)
		return nil, 80, nil
	}, func(string) error { t.Error("unmount called for a failed mount"); return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := Mount(ctx, mustURL(t, "smb://h/s"), "", "", Options{Guest: true}); err == nil {
		t.Fatal("expected the context error")
	}
	close(release)
	<-finished
	time.Sleep(50 * time.Millisecond)
}

func TestUnmount(t *testing.T) {
	var got string
	swap(t, nil, func(p string) error { got = p; return nil })
	if err := Unmount("/Volumes/share/"); err != nil || got != "/Volumes/share" {
		t.Errorf("%q, %v", got, err)
	}
	if err := Unmount("share"); !errors.Is(err, ErrBadMountpoint) {
		t.Errorf("relative mountpoint: %v", err)
	}
	swap(t, nil, func(string) error { return syscall.EBUSY })
	if err := Unmount("/Volumes/share"); !errors.Is(err, syscall.EBUSY) || !strings.Contains(err.Error(), "/Volumes/share") {
		t.Errorf("busy: %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int32
		is     error
		text   string
	}{
		{-128, ErrCanceled, "OSStatus -128"},
		{-5045, ErrPasswordNeedsChange, "password must be changed"},
		{-5046, ErrPasswordPolicy, "policy"},
		{-5999, ErrAccountRestricted, "restricted"},
		{-5998, ErrNoSharesAvailable, "no shares"},
		{-6003, ErrNoSharesAvailable, "no shares"},
		{-5997, ErrNoAuthMechanism, "authentication mechanism"},
		{-5996, ErrNoProtocolVersion, "protocol version"},
		{-6600, ErrInternal, "internal"},
		{-6602, ErrMountFailed, "mount failed"},
		{-6004, ErrGuestNotSupported, "guest"},
		{-6005, ErrAlreadyClosed, "closed"},
		{80, ErrAuth, "errno 80"},
		{int32(syscall.ENOENT), syscall.ENOENT, "errno 2"},
	}
	for _, c := range cases {
		err := &Error{Op: "mount", Status: c.status}
		if !errors.Is(err, c.is) {
			t.Errorf("status %d is not %v", c.status, c.is)
		}
		if !strings.Contains(err.Error(), c.text) || !strings.HasPrefix(err.Error(), "netfs: mount: ") {
			t.Errorf("status %d reads %q", c.status, err.Error())
		}
	}
	// EAUTH is also the errno.
	if !errors.Is(&Error{Status: 80}, syscall.Errno(80)) {
		t.Error("EAUTH lost its errno")
	}
	unknown := &Error{Op: "mount", Status: -1}
	if unknown.Unwrap() != nil || !strings.Contains(unknown.Error(), "unknown OSStatus -1") {
		t.Errorf("unknown status: %v", unknown)
	}
}

func TestErrUnsupportedIsStandard(t *testing.T) {
	if !errors.Is(ErrUnsupported, errors.ErrUnsupported) {
		t.Error("ErrUnsupported is not errors.ErrUnsupported")
	}
}
