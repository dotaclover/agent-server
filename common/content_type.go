package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

func RequireJSONContentType(c echo.Context) error {
	if c == nil || c.Request() == nil {
		return nil
	}
	contentType := strings.TrimSpace(c.Request().Header.Get(echo.HeaderContentType))
	if contentType == "" {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "content-type must be application/json")
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType != echo.MIMEApplicationJSON {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "content-type must be application/json")
	}
	return nil
}

func BindJSONStrict(c echo.Context, target interface{}) error {
	return bindJSONStrict(c, target, false)
}

func BindJSONStrictAllowEmpty(c echo.Context, target interface{}) error {
	return bindJSONStrict(c, target, true)
}

func bindJSONStrict(c echo.Context, target interface{}, allowEmpty bool) error {
	if c == nil || c.Request() == nil || c.Request().Body == nil {
		return fmt.Errorf("request body is required")
	}
	decoder := json.NewDecoder(c.Request().Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		if allowEmpty && err == io.EOF {
			return nil
		}
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	if !isJSONObject(raw) {
		return fmt.Errorf("request body must contain a JSON object")
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}
