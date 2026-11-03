package application

import "errors"

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("permission denied")
	ErrNotFound        = errors.New("not found")
)
