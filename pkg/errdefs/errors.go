// Package errdefs defines the common errors used throughout service.
//
// Use with errors.Wrap and error.Wrapf to add context to an error.
//
// To detect an error class, use the errors.Is functions to tell whether
// an error is of a certain type.
package errdefs

import (
	"errors"
)

var (
	ErrIllegalParameter = errors.New("ERR_ILLEGAL_PARAMETER")
	ErrUnauthorized     = errors.New("ERR_UNAUTHORIZED")
	ErrConvertFailed    = errors.New("ERR_CONVERT_FAILED")
	ErrAlreadyConverted = errors.New("ERR_ALREADY_CONVERTED")
	ErrUnhealthy        = errors.New("ERR_UNHEALTHY")
)
