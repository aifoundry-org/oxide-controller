package cluster

var _ error = &SecretKeyNotFoundError{}

type SecretKeyNotFoundError struct {
	m string
}

func (e *SecretKeyNotFoundError) Error() string {
	return e.m
}

func (e *SecretKeyNotFoundError) Is(target error) bool {
	if _, ok := target.(*SecretKeyNotFoundError); ok {
		return true
	}

	return false
}

func NewSecretKeyNotFoundError(key string) error {
	return &SecretKeyNotFoundError{
		m: "key '" + key + "' not found in secret",
	}
}
