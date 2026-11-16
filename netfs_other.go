// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build !darwin

package netfs

// There is no NetFS off macOS. The seams answer ErrUnsupported so that the
// validation in the portable file still runs, and a consumer cross-compiles
// without build tags of its own.
func init() {
	mountFn = func(request) ([]string, int32, error) { return nil, 0, ErrUnsupported }
	unmountFn = func(string) error { return ErrUnsupported }
}
