// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build darwin

package netfs_test

import (
	"errors"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	filesystem "github.com/go-filesystems/interface"
)

// memFS is a filesystem in a map for the real test to serve over SMB, with the
// whole-file interface only (no positional access). The server hands one
// Filesystem to every connection, so it has a lock.
type memFS struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{files: map[string][]byte{}, dirs: map[string]bool{"/": true}}
}

func (m *memFS) Close() error  { return nil }
func (m *memFS) Label() string { return "NETFS" }

func (m *memFS) ReadFile(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (m *memFS) WriteFile(p string, data []byte, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[path.Dir(p)] {
		return os.ErrNotExist
	}
	m.files[p] = append([]byte(nil), data...)
	return nil
}

func (m *memFS) ListDir(p string) ([]filesystem.DirEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[p] {
		return nil, os.ErrNotExist
	}
	var names []string
	for f := range m.files {
		if path.Dir(f) == p {
			names = append(names, path.Base(f))
		}
	}
	for d := range m.dirs {
		if d != "/" && path.Dir(d) == p {
			names = append(names, path.Base(d))
		}
	}
	sort.Strings(names)
	out := make([]filesystem.DirEntry, 0, len(names))
	for i, n := range names {
		t := uint8(1)
		if m.dirs[path.Join(p, n)] {
			t = 2
		}
		out = append(out, filesystem.NewDirEntry(uint64(i+2), n, t))
	}
	return out, nil
}

func (m *memFS) Stat(p string) (filesystem.Stat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs[p] {
		return filesystem.NewStat(0o040755, 0, 1), nil
	}
	if b, ok := m.files[p]; ok {
		return filesystem.NewStat(0o100644, uint64(len(b)), 2), nil
	}
	return nil, os.ErrNotExist
}

func (m *memFS) MkDir(p string, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[path.Dir(p)] {
		return os.ErrNotExist
	}
	if m.dirs[p] {
		return os.ErrExist
	}
	m.dirs[p] = true
	return nil
}

func (m *memFS) DeleteFile(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; !ok {
		return os.ErrNotExist
	}
	delete(m.files, p)
	return nil
}

func (m *memFS) DeleteDir(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[p] {
		return os.ErrNotExist
	}
	for f := range m.files {
		if strings.HasPrefix(f, p+"/") {
			return errors.New("directory not empty")
		}
	}
	delete(m.dirs, p)
	return nil
}

func (m *memFS) Rename(from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[from]
	if !ok {
		return os.ErrNotExist
	}
	delete(m.files, from)
	m.files[to] = b
	return nil
}

func (m *memFS) ReadLink(string) (string, error) { return "", os.ErrInvalid }
