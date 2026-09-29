package netlist

import "fmt"

// ErrorKind enumerates the distinguishable reasons a netlist can be rejected.
type ErrorKind string

const (
	ErrSyntax           ErrorKind = "syntax_error"
	ErrMissingField     ErrorKind = "missing_field"
	ErrInvalidValue     ErrorKind = "invalid_value"
	ErrUnknownDirective ErrorKind = "unknown_directive"
	ErrDuplicateDef     ErrorKind = "duplicate_definition"
	ErrUnknownNet       ErrorKind = "unknown_net"
	ErrUnknownComponent ErrorKind = "unknown_component"
	ErrUnknownPin       ErrorKind = "unknown_pin"
	ErrPinAlreadyJoined ErrorKind = "pin_already_joined"
	ErrNotFound         ErrorKind = "not_found"
	ErrConflict         ErrorKind = "conflict"
	ErrUnsupported      ErrorKind = "unsupported_change"
)

// ParseError points at a precise location in the input text.
type ParseError struct {
	Kind      ErrorKind
	Line      int
	Column    int
	Directive string
	Detail    string
}

func (e *ParseError) Error() string {
	if e.Directive != "" {
		return fmt.Sprintf("line %d:%d [%s] %s (%s)", e.Line, e.Column, e.Kind, e.Detail, e.Directive)
	}
	return fmt.Sprintf("line %d:%d [%s] %s", e.Line, e.Column, e.Kind, e.Detail)
}

// MutationError reports why a local mutation was rejected.
type MutationError struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *MutationError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Kind, e.Op, e.Detail)
}
