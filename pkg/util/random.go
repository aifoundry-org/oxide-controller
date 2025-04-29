package util

import (
	"crypto/rand"
)

const lowercaseCharset = "abcdefghijklmnopqrstuvwxyz"
const uppercaseCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
const numberCharset = "0123456789"

func RandomString(length int, lowercaseOnly bool) (string, error) {
	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	var charset string
	if lowercaseOnly {
		charset = lowercaseCharset + numberCharset
	} else {
		charset = lowercaseCharset + uppercaseCharset + numberCharset
	}

	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}
