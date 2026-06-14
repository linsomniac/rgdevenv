// Package txn applies management mutations through a single serialized staged
// transaction: snapshot → clone → build → validate → persist → publish → apply
// (§16).
package txn

import "errors"

// Sentinel kinds; the API maps them to HTTP status codes via errors.Is.
var (
	ErrValidation = errors.New("txn: validation error") // -> 400
	ErrConflict   = errors.New("txn: conflict")         // -> 409
	ErrNotFound   = errors.New("txn: not found")        // -> 404
)

// Error is a typed transaction error carrying a machine code and human message.
type Error struct {
	kind error // one of the sentinels above
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.kind }

// Validation/Conflict/NotFound build typed errors.
func Validation(code, msg string) *Error { return &Error{kind: ErrValidation, Code: code, Msg: msg} }
func Conflict(code, msg string) *Error   { return &Error{kind: ErrConflict, Code: code, Msg: msg} }
func NotFound(code, msg string) *Error   { return &Error{kind: ErrNotFound, Code: code, Msg: msg} }
