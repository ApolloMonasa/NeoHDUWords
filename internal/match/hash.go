package match

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func UniqueHash(stem string, options []string) string {
	opts := make([]string, len(options))
	copy(opts, options)
	sort.Strings(opts)

	var b strings.Builder
	b.Grow(len(stem) + 1 + len(opts)*16)
	b.WriteString(stem)
	b.WriteByte('\n')
	for i, o := range opts {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(o)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

