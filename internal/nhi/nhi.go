// Package nhi validates and generates New Zealand National Health Index
// numbers per HISO 10046.
//
// Two formats: legacy AAA999# (3 letters, 3 digits, numeric check digit)
// and AAA99A# (3 letters, 2 digits, 1 letter, alpha check character),
// first issued July 2026. The letter alphabet excludes I and O.
//
// Synthetic NHIs always start with Z, the prefix reserved for test data,
// so they can never collide with a real person's identifier.
package nhi

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

type Format string

const (
	FormatLegacy Format = "legacy"
	FormatNew    Format = "new"
)

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ"

var (
	pattern    = regexp.MustCompile(`^[A-HJ-NP-Z]{3}([0-9]{4}|[0-9]{2}[A-HJ-NP-Z]{2})$`)
	ErrInvalid = errors.New("nhi: invalid")
)

func Validate(s string) (Format, error) {
	s = strings.ToUpper(s)
	if !pattern.MatchString(s) {
		return "", ErrInvalid
	}
	sum := 0
	for i := 0; i < 6; i++ {
		sum += charValue(s[i]) * (7 - i)
	}
	check := s[6]
	if check >= '0' && check <= '9' {
		mod := sum % 11
		if mod == 0 || int(check-'0') != (11-mod)%10 {
			return "", ErrInvalid
		}
		return FormatLegacy, nil
	}
	if alphabet[23-sum%24] != check {
		return "", ErrInvalid
	}
	return FormatNew, nil
}

func charValue(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	return strings.IndexByte(alphabet, c) + 1
}

// GenerateSynthetic returns a valid NHI in the requested format,
// always within the Z test prefix.
func GenerateSynthetic(f Format) (string, error) {
	for i := 0; i < 1000; i++ {
		b := []byte{'Z', alphabet[randInt(24)], alphabet[randInt(24)],
			byte('0' + randInt(10)), byte('0' + randInt(10)), 0}
		if f == FormatNew {
			b[5] = alphabet[randInt(24)]
		} else {
			b[5] = byte('0' + randInt(10))
		}
		sum := 0
		for i := 0; i < 6; i++ {
			sum += charValue(b[i]) * (7 - i)
		}
		if f == FormatLegacy {
			mod := sum % 11
			if mod == 0 {
				continue // not allocatable; retry
			}
			return string(b) + string(byte('0'+(11-mod)%10)), nil
		}
		return string(b) + string(alphabet[23-sum%24]), nil
	}
	return "", errors.New("nhi: generation failed")
}

func randInt(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return v.Int64()
}
