package config

type notFoundError error

// IsNotFound tells if the error is due to non-existing key
func IsNotFound(err error) bool {
	_, ok := err.(notFoundError)
	return ok
}
