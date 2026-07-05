package main

import (
	"fmt"
	"go-agent-studio/cmd/commands"
	"go-agent-studio/services/buildinfo"
	"os"
)

var (
	AppName    = "Go Agent Studio"
	AppVersion = "2.0.0"
	BuildTime  = "dev"
)

func main() {
	buildinfo.Set(AppName, AppVersion, BuildTime)
	registry := commands.NewRegistry()
	commands.Register(registry, AppName, AppVersion, BuildTime)

	if err := registry.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
