package paygate

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// FeeFen returns fee in "fen" (1 yuan = 100 fen).
// Default is 0 (free). You can override by setting env COMPARE_JOB_FEE_FEN (non-negative integer).
func FeeFen() int64 {
	raw := strings.TrimSpace(os.Getenv("COMPARE_JOB_FEE_FEN"))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func readEnvDurationSecondsDefault(key string, defaultVal time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return time.Duration(n) * time.Second
}

