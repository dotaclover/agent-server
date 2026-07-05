package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const maxHTTPResponseBytes = 8 * 1024 * 1024

var (
	mcpBearerTokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	mcpAPIKeyPattern      = regexp.MustCompile(`(?i)(api[_-]?key["'\s:=]+)[A-Za-z0-9._~+/=-]+`)
	mcpSKKeyPattern       = regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{8,}\b`)
)

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, redactMCPError(e.Message))
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type content struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	JSON json.RawMessage `json:"json,omitempty"`
}

type transport interface {
	RoundTrip(ctx context.Context, req jsonRPCRequest) (*jsonRPCResponse, error)
	Notify(ctx context.Context, method string) error
	Close()
}

type Client struct {
	name      string
	transport transport
	nextID    atomic.Int64
}

func NewClient(name string) *Client {
	return &Client{name: name}
}

func (c *Client) StartStdio(ctx context.Context, command string, args []string) error {
	t, err := newStdioTransport(ctx, command, args)
	if err != nil {
		return err
	}
	c.transport = t
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return err
	}
	return nil
}

func (c *Client) StartHTTP(ctx context.Context, url, apiKey string) error {
	c.transport = &httpTransport{url: url, apiKey: apiKey, client: &http.Client{Timeout: 180 * time.Second}}
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return err
	}
	return nil
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "go-agent-studio", "version": "0.1.0"},
	}
	var result map[string]interface{}
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return fmt.Errorf("initialize mcp %s: %w", c.name, err)
	}
	return c.transport.Notify(ctx, "notifications/initialized")
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]interface{}{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (string, error) {
	var result struct {
		Content []content `json:"content"`
		IsError bool      `json:"isError,omitempty"`
	}
	if err := c.call(ctx, "tools/call", map[string]interface{}{"name": name, "arguments": arguments}, &result); err != nil {
		return "", err
	}
	var out string
	for _, item := range result.Content {
		if item.Text != "" {
			if out != "" {
				out += "\n"
			}
			out += item.Text
		} else if len(item.JSON) > 0 {
			if out != "" {
				out += "\n"
			}
			out += string(item.JSON)
		}
	}
	if result.IsError {
		return out, fmt.Errorf("mcp tool returned isError")
	}
	if out == "" {
		out = "MCP tool executed successfully."
	}
	return out, nil
}

func (c *Client) Close() {
	if c.transport != nil {
		c.transport.Close()
	}
}

func (c *Client) call(ctx context.Context, method string, params interface{}, result interface{}) error {
	id := c.nextID.Add(1)
	resp, err := c.transport.RoundTrip(ctx, jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("parse mcp result: %w", err)
		}
	}
	return nil
}

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
	closed  bool
}

func newStdioTransport(ctx context.Context, command string, args []string) (*stdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	t := &stdioTransport{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout)}
	t.scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *stdioTransport) RoundTrip(ctx context.Context, req jsonRPCRequest) (*jsonRPCResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, fmt.Errorf("stdio transport closed")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp stdio request: %w", err)
	}
	if _, err := fmt.Fprintf(t.stdin, "%s\n", data); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if !t.scanner.Scan() {
			if err := t.scanner.Err(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("mcp stdio process exited")
		}
		line := t.scanner.Bytes()
		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID == req.ID {
			return &resp, nil
		}
	}
}

func (t *stdioTransport) Notify(ctx context.Context, method string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if t.closed {
		return fmt.Errorf("stdio transport closed")
	}
	data, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method})
	if err != nil {
		return fmt.Errorf("marshal mcp stdio notification: %w", err)
	}
	_, err = fmt.Fprintf(t.stdin, "%s\n", data)
	return err
}

func (t *stdioTransport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_, _ = t.cmd.Process.Wait()
	}
}

type httpTransport struct {
	url    string
	apiKey string
	client *http.Client
}

func (t *httpTransport) RoundTrip(ctx context.Context, req jsonRPCRequest) (*jsonRPCResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp http request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}, nil
	}
	body, err := readLimitedHTTPBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp http status %d: %s", resp.StatusCode, truncateMCPError(redactMCPError(string(body)), 600))
	}
	var out jsonRPCResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (t *httpTransport) Notify(ctx context.Context, method string) error {
	data, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method})
	if err != nil {
		return fmt.Errorf("marshal mcp http notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := readLimitedHTTPBody(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp http notification status %d: %s", resp.StatusCode, truncateMCPError(redactMCPError(string(body)), 600))
	}
	return nil
}

func (t *httpTransport) Close() {}

func readLimitedHTTPBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxHTTPResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxHTTPResponseBytes {
		return nil, fmt.Errorf("mcp http response exceeds %d bytes", maxHTTPResponseBytes)
	}
	return data, nil
}

func redactMCPError(text string) string {
	text = mcpBearerTokenPattern.ReplaceAllString(text, "${1}[redacted]")
	text = mcpAPIKeyPattern.ReplaceAllString(text, "${1}[redacted]")
	text = mcpSKKeyPattern.ReplaceAllString(text, "sk-[redacted]")
	return text
}

func truncateMCPError(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}
