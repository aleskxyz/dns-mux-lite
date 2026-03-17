package main

import (
	"encoding/base32"
	"strings"
)

// base32StdEncode wraps StdEncoding.EncodeToString so scanner.go does not
// import encoding/base32 directly (helpful if we ever swap encoders).
func base32StdEncode(b []byte) string {
	return base32.StdEncoding.EncodeToString(b)
}

func dotifyBase32(payload string, maxLabel int) string {
	var labels []string
	for len(payload) > maxLabel {
		labels = append(labels, payload[:maxLabel])
		payload = payload[maxLabel:]
	}
	if len(payload) > 0 {
		labels = append(labels, payload)
	}
	return strings.Join(labels, ".")
}


