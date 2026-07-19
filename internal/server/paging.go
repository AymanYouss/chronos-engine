package server

import (
	"encoding/base64"
	"strconv"
)

// encodeOffset serializes a numeric offset into an opaque page token.
func encodeOffset(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeOffset parses a page token back into an offset, tolerating empty or
// malformed tokens by returning 0.
func decodeOffset(token string) int {
	if token == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
