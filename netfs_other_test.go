// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build !darwin

package netfs

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

func TestUnsupportedOffDarwin(t *testing.T) {
	u, _ := url.Parse("smb://127.0.0.1:4445/share")
	if _, err := Mount(context.Background(), u, "a", "b", Options{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Mount: %v", err)
	}
	if err := Unmount("/Volumes/share"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Unmount: %v", err)
	}
}
