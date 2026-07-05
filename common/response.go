package common

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type APIResponse struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

func Success(c echo.Context, message string, data interface{}) error {
	return c.JSON(http.StatusOK, APIResponse{Code: 0, Message: message, Data: data, RequestID: RequestID(c)})
}

func Fail(c echo.Context, status int, message string) error {
	return c.JSON(status, APIResponse{Code: status, Message: message, RequestID: RequestID(c)})
}

func RequestID(c echo.Context) string {
	if c == nil {
		return ""
	}
	if id, ok := c.Get("request_id").(string); ok && id != "" {
		return id
	}
	if id := c.Response().Header().Get(echo.HeaderXRequestID); id != "" {
		return id
	}
	return c.Request().Header.Get(echo.HeaderXRequestID)
}
