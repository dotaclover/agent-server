package buildinfo

import (
	"os"
	"sync"
)

const (
	defaultName    = "Go Agent Studio"
	defaultVersion = "1.1.0"
	defaultBuild   = "dev"
)

type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
}

var (
	mu      sync.RWMutex
	current = Info{Name: defaultName, Version: defaultVersion, Build: defaultBuild}
)

func Set(name, version, build string) {
	mu.Lock()
	defer mu.Unlock()
	current = normalize(Info{Name: name, Version: version, Build: build})
}

func Current() Info {
	mu.RLock()
	info := current
	mu.RUnlock()
	return fromEnv(info)
}

func normalize(info Info) Info {
	if info.Name == "" {
		info.Name = defaultName
	}
	if info.Version == "" {
		info.Version = defaultVersion
	}
	if info.Build == "" {
		info.Build = defaultBuild
	}
	return info
}

func fromEnv(info Info) Info {
	if value := os.Getenv("APP_NAME"); value != "" {
		info.Name = value
	}
	if value := os.Getenv("APP_VERSION"); value != "" {
		info.Version = value
	}
	if value := os.Getenv("APP_BUILD_TIME"); value != "" {
		info.Build = value
	} else if value := os.Getenv("APP_BUILD"); value != "" {
		info.Build = value
	}
	return normalize(info)
}
