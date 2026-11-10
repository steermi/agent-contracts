// Package problem is the error type of the skill file contracts
// (skill/inputs, skill/board): a stable code sentinel plus a human detail,
// so callers can show the detail without parsing the message.
package problem

import "fmt"

// Error is one contract violation. errors.Is(err, Code) holds.
type Error struct {
	Code   error
	Detail string
}

func (e *Error) Error() string { return e.Code.Error() + ": " + e.Detail }

// Unwrap exposes the code sentinel to errors.Is.
func (e *Error) Unwrap() error { return e.Code }

// New formats a detail under code.
func New(code error, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}
