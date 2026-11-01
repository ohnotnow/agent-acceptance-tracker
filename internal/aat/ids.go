package aat

import (
	"crypto/rand"
	"strings"
)

// Ids are random base62, minted locally with no coordination, so two
// developers' databases can later be merged without collisions (ant aat-VYQvH).
const (
	base62        = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	featureIDLen  = 6
	storyIDLen    = 6
	scenarioIDLen = 8
	questionIDLen = 6
)

func newID(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// 248 is the largest multiple of 62 below 256; rejecting the rest avoids modulo bias.
			if b < 248 && len(out) < n {
				out = append(out, base62[b%62])
			}
		}
	}
	return string(out), nil
}

func isID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(base62, r) {
			return false
		}
	}
	return true
}
