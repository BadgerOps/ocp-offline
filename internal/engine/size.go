package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize parses a human-readable size string like "25GB" into bytes.
// Supports B, KB, MB, GB, TB suffixes (case-insensitive).
// A plain number is treated as bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	s = strings.ToUpper(s)

	multipliers := []struct {
		suffix string
		mult   int64
	}{
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}

	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			numStr := strings.TrimSuffix(s, m.suffix)
			if numStr == "" {
				return 0, fmt.Errorf("missing number in size: %s", s)
			}
			n, err := strconv.ParseInt(numStr, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number in size %q: %w", s, err)
			}
			if n < 0 {
				return 0, fmt.Errorf("negative size: %s", s)
			}
			return n * m.mult, nil
		}
	}

	// Plain number = bytes
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative size: %s", s)
	}
	return n, nil
}
