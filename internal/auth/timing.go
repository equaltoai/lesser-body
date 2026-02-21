package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"math/big"
	"time"
)

func TimingSafeTokenValidation(providedToken, storedToken string) bool {
	maxLen := len(providedToken)
	if len(storedToken) > maxLen {
		maxLen = len(storedToken)
	}

	paddedProvided := padToLength(providedToken, maxLen)
	paddedStored := padToLength(storedToken, maxLen)

	result := subtle.ConstantTimeCompare([]byte(paddedProvided), []byte(paddedStored)) == 1

	constantTimeDelay()

	return result && len(providedToken) == len(storedToken)
}

func constantTimeDelay() {
	delay := time.Duration(secureRandInt(10)) * time.Millisecond
	time.Sleep(delay)
}

func padToLength(s string, length int) string {
	if len(s) >= length {
		return s[:length]
	}

	padded := make([]byte, length)
	copy(padded, s)
	return string(padded)
}

func secureRandInt(maxVal int) int {
	if maxVal <= 0 {
		return 0
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxVal)))
	if err != nil {
		return 0
	}

	return int(n.Int64())
}
