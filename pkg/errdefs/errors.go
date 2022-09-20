// Package errdefs defines the common errors used throughout service.
//
// Use with errors.Wrap and error.Wrapf to add context to an error.
//
// To detect an error class, use the errors.Is functions to tell whether
// an error is of a certain type.
package errdefs

import (
	"errors"
	"strings"
)

var (
	ErrIllegalParameter = errors.New("ERR_ILLEGAL_PARAMETER")
	ErrUnauthorized     = errors.New("ERR_UNAUTHORIZED")
	ErrConvertFailed    = errors.New("ERR_CONVERT_FAILED")
	ErrAlreadyConverted = errors.New("ERR_ALREADY_CONVERTED")
	ErrUnhealthy        = errors.New("ERR_UNHEALTHY")
)

// IsErrHTTPResponseToHTTPSClient returns whether err is
// "http: server gave HTTP response to HTTPS client"
func isErrHTTPResponseToHTTPSClient(err error) bool {
	// The error string is unexposed as of Go 1.16, so we can't use `errors.Is`.
	// https://github.com/golang/go/issues/44855
	const unexposed = "server gave HTTP response to HTTPS client"
	return strings.Contains(err.Error(), unexposed)
}

// IsErrConnectionRefused return whether err is
// "connect: connection refused"
func isErrConnectionRefused(err error) bool {
	const errMessage = "connect: connection refused"
	return strings.Contains(err.Error(), errMessage)
}

func NeedsRetryWithHTTP(err error) bool {
	return err != nil && (isErrHTTPResponseToHTTPSClient(err) || isErrConnectionRefused(err))
}
