package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var validPatternCharsRe = regexp.MustCompile(`^[a-zA-Z0-9*._:\-]+$`)

// ValidatePattern returns a human-readable error if pattern is not a valid
// routing pattern. Valid forms:
//
//	*              — catch-all
//	*.domain.com   — subdomain wildcard
//	*suffix        — suffix wildcard
//	prefix.*       — prefix wildcard
//	10.0.0.0/8     — IPv4 CIDR
//	hostname       — exact match
func ValidatePattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	if strings.ContainsAny(pattern, " \t\n\r") {
		return fmt.Errorf("pattern must not contain whitespace")
	}
	if strings.Contains(pattern, "/") {
		return validateCIDRPattern(pattern)
	}
	if !validPatternCharsRe.MatchString(pattern) {
		return fmt.Errorf("invalid characters — allowed: letters, digits, * . - _")
	}
	return nil
}

func validateCIDRPattern(pattern string) error {
	slash := strings.Index(pattern, "/")
	if slash == 0 {
		return fmt.Errorf("CIDR must have an address before '/'")
	}
	if slash == len(pattern)-1 {
		return fmt.Errorf("CIDR must have a prefix length after '/'")
	}
	prefixStr := pattern[slash+1:]
	prefix, err := strconv.Atoi(prefixStr)
	if err != nil || prefix < 0 || prefix > 32 {
		return fmt.Errorf("invalid prefix length %q — must be 0–32", prefixStr)
	}
	octets := strings.Split(pattern[:slash], ".")
	if len(octets) != 4 {
		return fmt.Errorf("CIDR address must have 4 octets, e.g. 10.0.0.0/8")
	}
	for _, o := range octets {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 || n > 255 {
			return fmt.Errorf("octet %q is out of range — must be 0–255", o)
		}
	}
	return nil
}
