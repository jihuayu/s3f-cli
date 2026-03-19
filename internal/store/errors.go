package store

import "errors"

var ErrNotFound = errors.New("object not found")

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
