// Public errs API; implementation lives in internal/errs.
package pumpfun

import (
	sdkErrs "github.com/vasyza/pumpfun-sdk/internal/errs"
)

type Error = sdkErrs.Error
type ErrorKind = sdkErrs.ErrorKind

const (
	KindInvalidArgument = sdkErrs.KindInvalidArgument
	KindNotFound        = sdkErrs.KindNotFound
	KindUnauthorized    = sdkErrs.KindUnauthorized
	KindRateLimited     = sdkErrs.KindRateLimited
	KindUnavailable     = sdkErrs.KindUnavailable
	KindTransport       = sdkErrs.KindTransport
	KindDecode          = sdkErrs.KindDecode
	KindRPC             = sdkErrs.KindRPC
	KindCanceled        = sdkErrs.KindCanceled
	KindUnsupported     = sdkErrs.KindUnsupported
	KindStreamActive    = sdkErrs.KindStreamActive
)

var (
	ErrInvalidArgument = sdkErrs.ErrInvalidArgument
	ErrNotFound        = sdkErrs.ErrNotFound
	ErrUnauthorized    = sdkErrs.ErrUnauthorized
	ErrRateLimited     = sdkErrs.ErrRateLimited
	ErrUnavailable     = sdkErrs.ErrUnavailable
	ErrTransport       = sdkErrs.ErrTransport
	ErrDecode          = sdkErrs.ErrDecode
	ErrRPC             = sdkErrs.ErrRPC
	ErrCanceled        = sdkErrs.ErrCanceled
	ErrUnsupported     = sdkErrs.ErrUnsupported
	ErrStreamActive    = sdkErrs.ErrStreamActive
)
