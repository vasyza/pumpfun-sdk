package errs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ErrorKind identifies an error without use of its message.
type ErrorKind string

const (
	KindInvalidArgument ErrorKind = "invalid_argument"
	KindNotFound        ErrorKind = "not_found"
	KindUnauthorized    ErrorKind = "unauthorized"
	KindRateLimited     ErrorKind = "rate_limited"
	KindUnavailable     ErrorKind = "unavailable"
	KindTransport       ErrorKind = "transport"
	KindDecode          ErrorKind = "decode"
	KindRPC             ErrorKind = "rpc"
	KindCanceled        ErrorKind = "canceled"
	KindUnsupported     ErrorKind = "unsupported"
	KindStreamActive    ErrorKind = "stream_active"
)

// Error contains safe error data. It does not contain URLs or response bodies.
type Error struct {
	Kind       ErrorKind     `json:"kind" yaml:"kind"`
	Operation  string        `json:"operation" yaml:"operation"`
	Message    string        `json:"message" yaml:"message"`
	StatusCode int           `json:"status_code,omitempty" yaml:"status_code,omitempty"`
	RPCCode    int           `json:"rpc_code,omitempty" yaml:"rpc_code,omitempty"`
	RetryAfter time.Duration `json:"-" yaml:"-"`
	Cause      error         `json:"-" yaml:"-"`
}

func (e *Error) Error() string {
	if e.Operation == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Message)
}

// Unwrap permits errors.Is to test context cancellation and timeouts.
func (e *Error) Unwrap() error { return e.Cause }

// Is compares error kinds. Use errors.Is with the errors below.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e.Kind == t.Kind
}

var (
	ErrInvalidArgument = &Error{Kind: KindInvalidArgument, Message: "The input is not valid."}
	ErrNotFound        = &Error{Kind: KindNotFound, Message: "The data was not found."}
	ErrUnauthorized    = &Error{Kind: KindUnauthorized, Message: "The server did not permit access."}
	ErrRateLimited     = &Error{Kind: KindRateLimited, Message: "The request rate is too high."}
	ErrUnavailable     = &Error{Kind: KindUnavailable, Message: "The server is not available."}
	ErrTransport       = &Error{Kind: KindTransport, Message: "The connection failed."}
	ErrDecode          = &Error{Kind: KindDecode, Message: "The response is not valid."}
	ErrRPC             = &Error{Kind: KindRPC, Message: "The RPC request failed."}
	ErrCanceled        = &Error{Kind: KindCanceled, Message: "The operation stopped."}
	ErrUnsupported     = &Error{Kind: KindUnsupported, Message: "The operation is not supported."}
	ErrStreamActive    = &Error{Kind: KindStreamActive, Message: "A stream is already active on this client."}
)

// Invalid reports an input error.
func Invalid(op, message string) error {
	return &Error{Kind: KindInvalidArgument, Operation: op, Message: message}
}

// Decode reports a response error.
func Decode(op string, cause error) error {
	return &Error{Kind: KindDecode, Operation: op, Message: "The response is not valid.", Cause: cause}
}

// Transport reports a connection error without its URL.
func Transport(op string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Context(op, err)
	}
	return &Error{Kind: KindTransport, Operation: op, Message: ErrTransport.Message, Cause: err}
}

// Context reports cancellation or a time limit.
func Context(op string, cause error) error {
	return &Error{Kind: KindCanceled, Operation: op, Message: "The operation stopped or its time limit expired.", Cause: cause}
}
