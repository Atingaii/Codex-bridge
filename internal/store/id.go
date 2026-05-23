package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func NewToken(prefix string) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	if prefix == "" {
		return token
	}
	return prefix + "_" + token
}

func CleanToken(value string) string {
	return strings.TrimSpace(value)
}
