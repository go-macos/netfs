// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build darwin

package netfs

import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/go-macos/objc"
)

// Framework is the NetFS framework binary.
const Framework = "/System/Library/Frameworks/NetFS.framework/NetFS"

const (
	kCFStringEncodingUTF8 = 0x08000100
	kCFNumberSInt32Type   = 3
	kCFURLPOSIXPathStyle  = 0
)

var (
	// NetFS.
	netfsMountURLSync func(url, mountpath, user, passwd, openOpts, mountOpts uintptr, mountpoints *uintptr) int32

	// CoreFoundation.
	cfRelease                    func(cf uintptr)
	cfStringCreate               func(alloc uintptr, s string, enc uint32) uintptr
	cfStringGetLength            func(s uintptr) int
	cfStringGetMaxSize           func(length int, enc uint32) int
	cfStringGetCString           func(s uintptr, buf *byte, size int, enc uint32) bool
	cfURLCreateWithString        func(alloc, s, base uintptr) uintptr
	cfURLCreateWithFileSystemPth func(alloc, path uintptr, style int, isDir bool) uintptr
	cfDictCreateMutable          func(alloc uintptr, capacity int, keyCB, valueCB uintptr) uintptr
	cfDictSetValue               func(d, k, v uintptr)
	cfNumberCreate               func(alloc uintptr, theType int, value unsafe.Pointer) uintptr
	cfArrayGetCount              func(a uintptr) int
	cfArrayGetValueAtIndex       func(a uintptr, i int) uintptr

	// Exported data: the address of the call-backs table, and the value of the
	// two CFBoolean variables.
	dictKeyCB, dictValueCB uintptr
	cfTrue, cfFalse        uintptr

	loadErr error
)

func init() {
	loadErr = bindAll(objc.CoreFoundation, Framework)
	mountFn = realMount
	unmountFn = realUnmount
}

// bindAll opens both frameworks and binds every entry point, returning the
// first failure. The paths are parameters so that the failure branches can be
// tested; nothing is assigned until a symbol resolves.
func bindAll(cfPath, nfPath string) error {
	cf, err := purego.Dlopen(cfPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("netfs: load CoreFoundation: %w", err)
	}
	nf, err := purego.Dlopen(nfPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("netfs: load NetFS: %w", err)
	}
	var firstErr error
	sym := func(h uintptr, name string) uintptr {
		if firstErr != nil {
			return 0
		}
		p, err := purego.Dlsym(h, name)
		if err != nil || p == 0 {
			firstErr = fmt.Errorf("netfs: missing symbol %s", name)
			return 0
		}
		return p
	}
	bind := func(fptr any, h uintptr, name string) {
		if p := sym(h, name); p != 0 {
			purego.RegisterFunc(fptr, p)
		}
	}

	bind(&netfsMountURLSync, nf, "NetFSMountURLSync")
	bind(&cfRelease, cf, "CFRelease")
	bind(&cfStringCreate, cf, "CFStringCreateWithCString")
	bind(&cfStringGetLength, cf, "CFStringGetLength")
	bind(&cfStringGetMaxSize, cf, "CFStringGetMaximumSizeForEncoding")
	bind(&cfStringGetCString, cf, "CFStringGetCString")
	bind(&cfURLCreateWithString, cf, "CFURLCreateWithString")
	bind(&cfURLCreateWithFileSystemPth, cf, "CFURLCreateWithFileSystemPath")
	bind(&cfDictCreateMutable, cf, "CFDictionaryCreateMutable")
	bind(&cfDictSetValue, cf, "CFDictionarySetValue")
	bind(&cfNumberCreate, cf, "CFNumberCreate")
	bind(&cfArrayGetCount, cf, "CFArrayGetCount")
	bind(&cfArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")

	// dlsym of a data symbol is the address of the variable. For the callback
	// tables that address is what CFDictionaryCreateMutable wants; for the
	// CFBoolean variables it is read once, through the address of a local so
	// that go vet's unsafeptr check has nothing to reject.
	dictKeyCB = sym(cf, "kCFTypeDictionaryKeyCallBacks")
	dictValueCB = sym(cf, "kCFTypeDictionaryValueCallBacks")
	t, f := sym(cf, "kCFBooleanTrue"), sym(cf, "kCFBooleanFalse")
	if firstErr == nil {
		cfTrue = deref(t)
		cfFalse = deref(f)
	}
	return firstErr
}

// deref reads the pointer-sized value stored at the address a.
func deref(a uintptr) uintptr { return **(**uintptr)(unsafe.Pointer(&a)) }

func cfString(s string) uintptr { return cfStringCreate(0, s, kCFStringEncodingUTF8) }

// goString copies a CFString into a Go string.
func goString(s uintptr) string {
	buf := make([]byte, cfStringGetMaxSize(cfStringGetLength(s), kCFStringEncodingUTF8)+1)
	// The buffer is sized by CFStringGetMaximumSizeForEncoding plus a NUL, so
	// the call cannot run out of room; the result is the bytes before the NUL.
	cfStringGetCString(s, &buf[0], len(buf), kCFStringEncodingUTF8)
	return string(bytes.TrimRight(buf, "\x00"))
}

// arrayStrings copies a CFArray of CFStrings (the mountpoints NetFS returns).
func arrayStrings(a uintptr) []string {
	n := cfArrayGetCount(a)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, goString(cfArrayGetValueAtIndex(a, i)))
	}
	return out
}

// newDict builds a CFMutableDictionary from string keys and string, bool or
// int32 values. The caller owns the result and every object in it is released
// by the dictionary's own call-backs when the dictionary is.
func newDict(m map[string]any) (uintptr, error) {
	d := cfDictCreateMutable(0, len(m), dictKeyCB, dictValueCB)
	for k, v := range m {
		key := cfString(k)
		var val uintptr
		switch x := v.(type) {
		case string:
			val = cfString(x)
		case bool:
			// The CFBoolean constants are not owned; take no reference to release.
			val = cfFalse
			if x {
				val = cfTrue
			}
			cfDictSetValue(d, key, val)
			cfRelease(key)
			continue
		case int32:
			val = cfNumberCreate(0, kCFNumberSInt32Type, unsafe.Pointer(&x))
		default:
			cfRelease(key)
			cfRelease(d)
			return 0, fmt.Errorf("netfs: option %q has unsupported type %T", k, v)
		}
		cfDictSetValue(d, key, val)
		cfRelease(key)
		cfRelease(val)
	}
	return d, nil
}

// realMount builds the CoreFoundation arguments, calls NetFSMountURLSync and
// releases everything it created, including the returned mountpoints array.
func realMount(r request) ([]string, int32, error) {
	if loadErr != nil {
		return nil, 0, loadErr
	}
	urlStr := cfString(r.url)
	defer cfRelease(urlStr)
	u := cfURLCreateWithString(0, urlStr, 0)
	if u == 0 {
		return nil, 0, fmt.Errorf("%w (CFURL refused %q)", ErrBadURL, r.url)
	}
	defer cfRelease(u)

	var mountDir uintptr
	if r.mountDir != "" {
		p := cfString(r.mountDir)
		defer cfRelease(p)
		mountDir = cfURLCreateWithFileSystemPth(0, p, kCFURLPOSIXPathStyle, true)
		defer cfRelease(mountDir)
	}
	var user, pass uintptr
	if r.user != "" {
		user = cfString(r.user)
		defer cfRelease(user)
	}
	if r.password != "" {
		pass = cfString(r.password)
		defer cfRelease(pass)
	}
	open, err := newDict(r.open)
	if err != nil {
		return nil, 0, err
	}
	defer cfRelease(open)
	mnt, err := newDict(r.mount)
	if err != nil {
		return nil, 0, err
	}
	defer cfRelease(mnt)

	var points uintptr
	status := netfsMountURLSync(u, mountDir, user, pass, open, mnt, &points)
	if points == 0 {
		return nil, status, nil
	}
	// NetFS.h does not say who owns the returned array. It is treated as
	// owned by the caller (released here): the real mount test releases it
	// on every mount without a crash, which an unowned array would not survive.
	defer cfRelease(points)
	return arrayStrings(points), status, nil
}

func realUnmount(path string) error { return syscall.Unmount(path, 0) }
