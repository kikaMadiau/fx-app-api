package storage

import (
	"fmt"
	"strings"
	"unicode"
)

func NormalizePhone(phone string) (string, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return "", fmt.Errorf("phone is required")
	}

	var builder strings.Builder
	for i, r := range phone {
		switch {
		case unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == '+' && i == 0:
			builder.WriteRune(r)
		case unicode.IsSpace(r) || r == '-' || r == '(' || r == ')' || r == '.':
			continue
		default:
			return "", fmt.Errorf("phone contains invalid characters")
		}
	}

	normalized := builder.String()
	if strings.HasPrefix(normalized, "00") {
		normalized = "+" + strings.TrimPrefix(normalized, "00")
	}
	if normalized == "+" || normalized == "" {
		return "", fmt.Errorf("phone is invalid")
	}

	return normalized, nil
}
