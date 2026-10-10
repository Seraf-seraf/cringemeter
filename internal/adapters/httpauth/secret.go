package httpauth

import (
	"crypto/sha256"
	"crypto/subtle"
)

func Matches(expected, actual string) bool {
	want, got := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(actual))
	return expected != "" && subtle.ConstantTimeCompare(want[:], got[:]) == 1
}
