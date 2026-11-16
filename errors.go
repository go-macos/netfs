// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package netfs

import (
	"errors"
	"fmt"
	"syscall"
)

// NetFS's return value is two vocabularies in one int (NetFS.h): positive is an
// errno, negative is an OSStatus, "extended to include" the codes below from
// NetFS.h itself and from <NetAuth/NetAuthErrors.h>.
var (
	ErrCanceled            = errors.New("canceled")                                // -128 userCanceledErr
	ErrPasswordNeedsChange = errors.New("password must be changed")                // -5045 ENETFSPWDNEEDSCHANGE
	ErrPasswordPolicy      = errors.New("password refused by the server's policy") // -5046 ENETFSPWDPOLICY
	ErrAccountRestricted   = errors.New("account restricted")                      // -5999 ENETFSACCOUNTRESTRICTED
	ErrNoSharesAvailable   = errors.New("no shares available")                     // -5998 ENETFSNOSHARESAVAIL, -6003
	ErrNoAuthMechanism     = errors.New("no usable authentication mechanism")      // -5997 ENETFSNOAUTHMECHSUPP
	ErrNoProtocolVersion   = errors.New("no usable protocol version")              // -5996 ENETFSNOPROTOVERSSUPP
	ErrInternal            = errors.New("NetAuth internal error")                  // -6600 kNetAuthErrorInternal
	ErrMountFailed         = errors.New("mount failed")                            // -6602 kNetAuthErrorMountFailed
	ErrGuestNotSupported   = errors.New("guest access not supported")              // -6004 kNetAuthErrorGuestNotSupported
	ErrAlreadyClosed       = errors.New("session already closed")                  // -6005 kNetAuthErrorAlreadyClosed
	// ErrAuth is EAUTH (80), what NetFS answers for a wrong user or password.
	ErrAuth = errors.New("authentication failed")
)

// eauth is EAUTH on macOS. It is spelled as a number because syscall.EAUTH
// does not exist off darwin and the mapping is portable.
const eauth = 80

var statusErrors = map[int32]error{
	-128:  ErrCanceled,
	-5045: ErrPasswordNeedsChange,
	-5046: ErrPasswordPolicy,
	-5999: ErrAccountRestricted,
	-5998: ErrNoSharesAvailable,
	-5997: ErrNoAuthMechanism,
	-5996: ErrNoProtocolVersion,
	-6600: ErrInternal,
	-6602: ErrMountFailed,
	-6003: ErrNoSharesAvailable,
	-6004: ErrGuestNotSupported,
	-6005: ErrAlreadyClosed,
}

// Error is a failed NetFS call: Status is the raw return value.
type Error struct {
	Op     string
	Status int32
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("netfs: %s: %v (errno %d)", e.Op, syscall.Errno(e.Status), e.Status)
	}
	if err, ok := statusErrors[e.Status]; ok {
		return fmt.Sprintf("netfs: %s: %v (OSStatus %d)", e.Op, err, e.Status)
	}
	return fmt.Sprintf("netfs: %s: unknown OSStatus %d", e.Op, e.Status)
}

// Unwrap exposes the sentinel for an OSStatus, and the syscall.Errno for a
// positive status, plus ErrAuth for EAUTH.
func (e *Error) Unwrap() []error {
	switch {
	case e.Status == eauth:
		return []error{ErrAuth, syscall.Errno(e.Status)}
	case e.Status > 0:
		return []error{syscall.Errno(e.Status)}
	}
	if err, ok := statusErrors[e.Status]; ok {
		return []error{err}
	}
	return nil
}
