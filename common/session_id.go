package common

import (
	"errors"
	"fmt"
)

const MaxSessionIDLength = 96

func ValidateOptionalSessionID(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return ValidateRequiredSessionID(sessionID)
}

func ValidateRequiredSessionID(sessionID string) error {
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if len(sessionID) > MaxSessionIDLength {
		return fmt.Errorf("session_id is too long; max %d characters", MaxSessionIDLength)
	}
	for i := 0; i < len(sessionID); i++ {
		if isSessionIDChar(sessionID[i]) {
			continue
		}
		return errors.New("session_id contains invalid characters")
	}
	return nil
}

func isSessionIDChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '_' || ch == '-'
}
