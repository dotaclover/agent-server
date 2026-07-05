package llm

import (
	"fmt"
	"io"
)

const MaxHTTPResponseBytes int64 = 2 * 1024 * 1024

func ReadLimitedBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	limited := io.LimitReader(body, MaxHTTPResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxHTTPResponseBytes {
		return nil, fmt.Errorf("http response exceeds %d bytes", MaxHTTPResponseBytes)
	}
	return data, nil
}
