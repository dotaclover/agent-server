package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxMCPRegisteredToolNameLength = 64

var mcpNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type ConfigFile struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

type ServerConfig struct {
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	URL         string   `json:"url,omitempty"`
	APIKey      string   `json:"apiKey,omitempty"`
	Description string   `json:"description,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
}

type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

func NewManager() *Manager {
	return &Manager{clients: map[string]*Client{}}
}

func (m *Manager) LoadConfig(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cfg ConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse mcp config: %w", err)
	}
	names := sortedServerNames(cfg.Servers)
	for _, name := range names {
		server := cfg.Servers[name]
		if err := validateServerConfig(name, server); err != nil {
			return err
		}
	}

	newClients := map[string]*Client{}
	for _, name := range names {
		server := cfg.Servers[name]
		if server.Disabled {
			continue
		}
		client := NewClient(name)
		if server.URL != "" {
			err = client.StartHTTP(ctx, server.URL, server.APIKey)
		} else if server.Command != "" {
			err = client.StartStdio(ctx, server.Command, server.Args)
		} else {
			continue
		}
		if err != nil {
			closeClients(newClients)
			return fmt.Errorf("connect mcp %s: %w", name, err)
		}
		newClients[name] = client
	}

	m.mu.Lock()
	oldClients := m.clients
	m.clients = newClients
	m.mu.Unlock()
	closeClients(oldClients)
	return nil
}

func sortedServerNames(servers map[string]ServerConfig) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateServerConfig(name string, server ServerConfig) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("mcp server name cannot be blank")
	}
	if !isValidMCPName(name) {
		return fmt.Errorf("mcp server name %q is invalid; use only letters, digits, underscore, or hyphen", name)
	}
	if server.Disabled {
		return nil
	}
	hasURL := strings.TrimSpace(server.URL) != ""
	hasCommand := strings.TrimSpace(server.Command) != ""
	if hasURL == hasCommand {
		return fmt.Errorf("mcp server %s must configure exactly one of url or command", name)
	}
	if server.URL != strings.TrimSpace(server.URL) {
		return fmt.Errorf("mcp server %s url cannot have leading or trailing whitespace", name)
	}
	if server.Command != strings.TrimSpace(server.Command) {
		return fmt.Errorf("mcp server %s command cannot have leading or trailing whitespace", name)
	}
	if hasURL {
		parsed, err := url.Parse(server.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("mcp server %s url must be an absolute http(s) URL without userinfo, query, or fragment", name)
		}
	}
	return nil
}

func (m *Manager) RegisterTools(ctx context.Context, registry *aitypes.ToolRegistry) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for serverName, client := range m.clients {
		tools, err := client.ListTools(ctx)
		if err != nil {
			return fmt.Errorf("list mcp tools %s: %w", serverName, err)
		}
		for _, tool := range tools {
			if err := validateToolName(serverName, tool); err != nil {
				return err
			}
			registry.Register(convertTool(serverName, client, tool))
		}
	}
	return nil
}

// CopyTools copies all MCP tools from src to dst. Use this to selectively expose
// MCP tools to lower-privilege registries (e.g. public, operator).
func (m *Manager) CopyTools(dst, src *aitypes.ToolRegistry) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for serverName, client := range m.clients {
		tools, err := client.ListTools(context.Background())
		if err != nil {
			continue
		}
		for _, tool := range tools {
			name := serverName + "_" + tool.Name
			if t, ok := src.Get(name); ok {
				dst.Register(t)
			}
		}
	}
}

func (m *Manager) Status() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	sort.Strings(names)

	type serverDetail struct {
		Name      string `json:"name"`
		Transport string `json:"transport"`
		ToolCount int    `json:"tool_count"`
		Status    string `json:"status"`
	}

	serversDetail := make([]serverDetail, 0, len(names))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for _, name := range names {
		client := m.clients[name]
		transportType := "stdio"
		if client.transport != nil {
			switch client.transport.(type) {
			case *httpTransport:
				transportType = "http"
			case *stdioTransport:
				transportType = "stdio"
			}
		}

		toolCount := 0
		if client.transport != nil {
			if tools, err := client.ListTools(ctx); err == nil {
				toolCount = len(tools)
			}
		}

		serversDetail = append(serversDetail, serverDetail{
			Name:      name,
			Transport: transportType,
			ToolCount: toolCount,
			Status:    "connected",
		})
	}

	return map[string]interface{}{
		"servers":        names,
		"servers_detail": serversDetail,
		"count":          len(names),
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	closeClients(m.clients)
	m.clients = map[string]*Client{}
}

func closeClients(clients map[string]*Client) {
	for _, client := range clients {
		client.Close()
	}
}

func convertTool(serverName string, client *Client, mt Tool) *aitypes.Tool {
	name := serverName + "_" + mt.Name
	schema := string(mt.InputSchema)
	if schema == "" {
		schema = `{"type":"object","properties":{}}`
	}
	return &aitypes.Tool{
		Name:        name,
		Description: fmt.Sprintf("[MCP/%s] %s", serverName, mt.Description),
		Parameters:  schema,
		Destructive: true,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			return client.CallTool(ctx, mt.Name, args)
		},
	}
}

func validateToolName(serverName string, tool Tool) error {
	if strings.TrimSpace(tool.Name) == "" {
		return fmt.Errorf("mcp tool name from server %s cannot be blank", serverName)
	}
	if !isValidMCPName(tool.Name) {
		return fmt.Errorf("mcp tool %q from server %s has invalid name; use only letters, digits, underscore, or hyphen", tool.Name, serverName)
	}
	registeredName := serverName + "_" + tool.Name
	if len(registeredName) > maxMCPRegisteredToolNameLength {
		return fmt.Errorf("mcp tool %q from server %s registers as %q, exceeding %d characters", tool.Name, serverName, registeredName, maxMCPRegisteredToolNameLength)
	}
	return nil
}

func isValidMCPName(name string) bool {
	return mcpNamePattern.MatchString(name)
}
