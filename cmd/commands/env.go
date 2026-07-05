package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

type dotEnvLoadResult struct {
	CWD     string
	Path    string
	Loaded  bool
	Keys    int
	Applied int
	Error   string
}

func loadDotEnv() dotEnvLoadResult {
	cwd, err := os.Getwd()
	if err != nil {
		return dotEnvLoadResult{Error: err.Error()}
	}

	path := filepath.Join(cwd, ".env")
	if dotenvDisabled() {
		return dotEnvLoadResult{CWD: cwd, Path: path, Error: "disabled by APP_LOAD_DOTENV=false"}
	}
	values, err := readDotEnv(path)
	if err != nil {
		return dotEnvLoadResult{CWD: cwd, Path: path, Error: err.Error()}
	}

	return dotEnvLoadResult{
		CWD:     cwd,
		Path:    path,
		Loaded:  true,
		Keys:    len(values),
		Applied: applyEnvValues(values),
	}
}

func readDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	return godotenv.Unmarshal(string(data))
}

func applyEnvValues(values map[string]string) int {
	applied := 0
	for key, value := range values {
		current, exists := os.LookupEnv(key)
		if !exists || current == "" {
			_ = os.Setenv(key, value)
			applied++
		}
	}
	return applied
}

func dotenvDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_LOAD_DOTENV"))) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}
