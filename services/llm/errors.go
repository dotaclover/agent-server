package llm

import (
	"encoding/json"
	"fmt"
	"go-agent-studio/services/security"
	"strings"
)

type apiErrorDetail struct {
	Message string
	Type    string
	Code    string
}

func APIStatusError(provider string, statusCode int, body []byte) error {
	return apiStatusError(provider, statusCode, body)
}

func RedactSensitive(text string) string {
	return security.RedactSensitive(text)
}

func apiStatusError(provider string, statusCode int, body []byte) error {
	detail := extractAPIErrorDetail(body)
	if strings.TrimSpace(detail.Message) == "" {
		detail.Message = strings.TrimSpace(string(body))
	}
	if strings.TrimSpace(detail.Message) == "" {
		detail.Message = "empty response body"
	}
	detail.Message = truncate(security.RedactSensitive(detail.Message), 600)
	if detail.Type != "" || detail.Code != "" {
		attrs := make([]string, 0, 2)
		if detail.Type != "" {
			attrs = append(attrs, "type="+security.RedactSensitive(detail.Type))
		}
		if detail.Code != "" {
			attrs = append(attrs, "code="+security.RedactSensitive(detail.Code))
		}
		return fmt.Errorf("%s api status %d (%s): %s", provider, statusCode, strings.Join(attrs, " "), detail.Message)
	}
	return fmt.Errorf("%s api status %d: %s", provider, statusCode, detail.Message)
}

func extractAPIErrorDetail(body []byte) apiErrorDetail {
	var envelope struct {
		Type    string      `json:"type"`
		Message string      `json:"message"`
		Code    interface{} `json:"code"`
		Error   struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Code    interface{} `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return apiErrorDetail{}
	}
	detail := apiErrorDetail{
		Message: envelope.Error.Message,
		Type:    envelope.Error.Type,
		Code:    stringifyErrorCode(envelope.Error.Code),
	}
	if detail.Message == "" {
		detail.Message = envelope.Message
	}
	if detail.Type == "" {
		detail.Type = envelope.Type
	}
	if detail.Code == "" {
		detail.Code = stringifyErrorCode(envelope.Code)
	}
	if detail.Type == "error" && envelope.Error.Type != "" {
		detail.Type = envelope.Error.Type
	}
	return detail
}

func stringifyErrorCode(code interface{}) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprint(v)
	}
}
