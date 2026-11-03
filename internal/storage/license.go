package storage

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// NormalizeBCCLicense applies the configured license format without assuming a BCC format.
func NormalizeBCCLicense(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "", fmt.Errorf("BCC license number is required")
	}
	pattern := strings.TrimSpace(os.Getenv("BCC_LICENSE_PATTERN"))
	if pattern == "" {
		return value, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid BCC_LICENSE_PATTERN: %w", err)
	}
	if !re.MatchString(value) {
		return "", fmt.Errorf("BCC license number has invalid format")
	}
	return value, nil
}
