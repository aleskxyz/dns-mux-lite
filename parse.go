package main

import "strings"

// parseResolver parses a single resolver string into a Resolver.
// Accepted formats:
//   - "IP"          -> IP:53
//   - "IP:port"     -> IP:port
func parseResolver(value string) (Resolver, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Resolver{}, false
	}
	if strings.Contains(value, ":") {
		return Resolver{Addr: value}, true
	}
	return Resolver{Addr: value + ":53"}, true
}

