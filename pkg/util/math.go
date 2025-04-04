package util

import (
	"golang.org/x/exp/constraints"
)

func RoundUp[T constraints.Integer](n, multiple T) T {
	if multiple == 0 {
		return n
	}
	remainder := n % multiple
	if remainder == 0 {
		return n
	}
	return n + multiple - remainder
}
