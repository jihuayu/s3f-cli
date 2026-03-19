package model

import "fmt"

type ErrorCode string

const (
	ErrPathNotFound         ErrorCode = "path_not_found"
	ErrObjectNotFound       ErrorCode = "object_not_found"
	ErrEmptyDirectory       ErrorCode = "empty_directory"
	ErrNonAtomicMove        ErrorCode = "non_atomic_move"
	ErrRemoteChanged        ErrorCode = "remote_changed"
	ErrInvalidPath          ErrorCode = "invalid_path"
	ErrUnsupportedOperation ErrorCode = "unsupported_operation"
)

type Error struct {
	Code    ErrorCode
	Op      string
	Path    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	base := string(e.Code)
	if e.Op != "" {
		base = e.Op + ": " + base
	}
	if e.Path != "" {
		base += " (" + e.Path + ")"
	}
	if e.Message != "" {
		base += ": " + e.Message
	}
	if e.Err != nil {
		base += ": " + e.Err.Error()
	}
	return base
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewError(code ErrorCode, op, path, message string, err error) *Error {
	return &Error{
		Code:    code,
		Op:      op,
		Path:    path,
		Message: message,
		Err:     err,
	}
}

func Unsupported(op, path, message string) error {
	return NewError(ErrUnsupportedOperation, op, path, message, nil)
}

func InvalidPath(op, path, message string) error {
	return NewError(ErrInvalidPath, op, path, message, nil)
}

func Wrapf(code ErrorCode, op, path, format string, args ...any) error {
	return NewError(code, op, path, fmt.Sprintf(format, args...), nil)
}
