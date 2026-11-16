// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package netfs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ErrUnsupported is returned by every operation off darwin. It is
// [errors.ErrUnsupported], so errors.Is(err, errors.ErrUnsupported) also holds.
var ErrUnsupported = errors.ErrUnsupported

var (
	// ErrBadURL: the URL is nil, has an unsupported scheme, or has no host.
	ErrBadURL = errors.New("netfs: URL must be smb://, http:// or https:// with a host")
	// ErrCredentialsInURL: user information in the URL. Credentials travel
	// through the arguments of Mount, never through a URL, which ends up in
	// logs, error messages and the volume's mounted-from name.
	ErrCredentialsInURL = errors.New("netfs: credentials must be passed as arguments, not in the URL")
	// ErrBadMountDir: Options.MountDir is not an absolute path.
	ErrBadMountDir = errors.New("netfs: MountDir must be an absolute path")
	// ErrBadMountpoint: the path given to Unmount is not an absolute path.
	ErrBadMountpoint = errors.New("netfs: mountpoint must be an absolute path")
	// ErrNoMountpoint: NetFS reported success and returned no mountpoint.
	ErrNoMountpoint = errors.New("netfs: mount succeeded but returned no mountpoint")
	// ErrPasswordWithoutUser: a password with no user name.
	ErrPasswordWithoutUser = errors.New("netfs: a password needs a user")
)

// Options tunes a mount. The zero value mounts read-write, visible in Finder,
// under /Volumes, and refuses loopback servers.
type Options struct {
	// MountDir is the directory the mountpoint is created below. Empty means
	// NetFS's default, /Volumes, which is where Finder looks.
	MountDir string
	// ReadOnly mounts with MNT_RDONLY.
	ReadOnly bool
	// Hidden mounts with MNT_DONTBROWSE: the volume is not shown in Finder or
	// in /Volumes listings of pickers. Leave it false for the sidebar.
	Hidden bool
	// Guest logs in as a guest; user and password are then ignored by NetFS.
	Guest bool
	// AllowLoopback permits a server on this machine (127.0.0.1, ::1).
	// NetFS.h documents kNetFSAllowLoopbackKey as "Allow a loopback mount".
	// (Measured on macOS 27, 127.0.0.1 also mounted without it; set it
	// anyway, since that is the documented contract.)
	AllowLoopback bool
	// AllowSubMounts permits mounting a directory below the share point.
	AllowSubMounts bool
}

// The CFDictionary keys and values NetFS.h defines, spelled as the header
// spells them. A typo here is silent in C, so tests pin them.
const (
	keyUIOption       = "UIOption"
	valueNoUI         = "NoUI"
	keyGuest          = "Guest"
	keyAllowLoopback  = "AllowLoopback"
	keyMountFlags     = "MountFlags"
	keyAllowSubMounts = "AllowSubMounts"
)

// sys/mount.h.
const (
	mntRdonly     = 0x00000001
	mntDontBrowse = 0x00100000
)

// buildOpenOptions is the open_options dictionary of NetFSMountURLSync.
// NoUI is always set: a library must never raise an authentication dialog.
func buildOpenOptions(o Options) map[string]any {
	m := map[string]any{keyUIOption: valueNoUI}
	if o.Guest {
		m[keyGuest] = true
	}
	if o.AllowLoopback {
		m[keyAllowLoopback] = true
	}
	return m
}

// buildMountOptions is the mount_options dictionary. MountFlags is an int32
// (a CFNumber SInt32) holding MNT_ flags.
func buildMountOptions(o Options) map[string]any {
	m := map[string]any{}
	var flags int32
	if o.ReadOnly {
		flags |= mntRdonly
	}
	if o.Hidden {
		flags |= mntDontBrowse
	}
	if flags != 0 {
		m[keyMountFlags] = flags
	}
	if o.AllowSubMounts {
		m[keyAllowSubMounts] = true
	}
	return m
}

// request is everything one NetFSMountURLSync call takes.
type request struct {
	url      string
	mountDir string
	user     string
	password string
	open     map[string]any
	mount    map[string]any
}

// checkURL validates u and returns the string handed to CFURLCreateWithString.
func checkURL(u *url.URL) (string, error) {
	if u == nil {
		return "", ErrBadURL
	}
	switch strings.ToLower(u.Scheme) {
	case "smb", "http", "https":
	default:
		return "", fmt.Errorf("%w (scheme %q)", ErrBadURL, u.Scheme)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("%w (no host)", ErrBadURL)
	}
	if u.User != nil {
		return "", ErrCredentialsInURL
	}
	return u.String(), nil
}

// newRequest validates the arguments of Mount and builds the request.
func newRequest(u *url.URL, user, password string, opts Options) (request, error) {
	s, err := checkURL(u)
	if err != nil {
		return request{}, err
	}
	if password != "" && user == "" {
		return request{}, ErrPasswordWithoutUser
	}
	if opts.MountDir != "" && !filepath.IsAbs(opts.MountDir) {
		return request{}, ErrBadMountDir
	}
	return request{
		url:      s,
		mountDir: opts.MountDir,
		user:     user,
		password: password,
		open:     buildOpenOptions(opts),
		mount:    buildMountOptions(opts),
	}, nil
}

// Seams: the platform files point them at the real calls.
var (
	// mountFn runs NetFSMountURLSync. status is its return value; err is a
	// failure to make the call at all.
	mountFn func(request) (points []string, status int32, err error)
	// unmountFn runs unmount(2).
	unmountFn func(path string) error
)

// Mount mounts u through NetFSMountURLSync and returns the first mountpoint,
// which is a directory under /Volumes unless Options.MountDir says otherwise.
//
// user and password go to NetFS as CFStrings; they are never placed in a URL,
// an argument vector or the environment, and NetFS is told NoUI so that no
// dialog can appear. u must be smb://, http:// or https:// (WebDAV) with a
// host and no user information.
//
// The call is synchronous inside NetFS. If ctx ends first, Mount returns
// ctx.Err() and a mount that completes afterwards is unmounted again.
func Mount(ctx context.Context, u *url.URL, user, password string, opts Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	req, err := newRequest(u, user, password, opts)
	if err != nil {
		return "", err
	}
	type result struct {
		points []string
		status int32
		err    error
	}
	done := make(chan result, 1)
	go func() {
		p, s, e := mountFn(req)
		done <- result{p, s, e}
	}()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		go func() {
			if late := <-done; late.err == nil && late.status == 0 && len(late.points) > 0 {
				_ = unmountFn(late.points[0])
			}
		}()
		return "", ctx.Err()
	}
	if r.err != nil {
		return "", r.err
	}
	if r.status != 0 {
		return "", &Error{Op: "mount", Status: r.status}
	}
	if len(r.points) == 0 {
		return "", ErrNoMountpoint
	}
	return r.points[0], nil
}

// Unmount unmounts the volume mounted at mountpoint with unmount(2). It fails
// with EBUSY while a file on the volume is open.
func Unmount(mountpoint string) error {
	if !filepath.IsAbs(mountpoint) {
		return ErrBadMountpoint
	}
	if err := unmountFn(filepath.Clean(mountpoint)); err != nil {
		return fmt.Errorf("netfs: unmount %s: %w", mountpoint, err)
	}
	return nil
}
