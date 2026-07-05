package commands

import (
	"fmt"
	"sort"
	"strings"
)

type Command interface {
	Name() string
	Description() string
	Execute(args []string) error
}

type Registry struct {
	commands map[string]Command
	appName  string
	version  string
	build    string
}

func NewRegistry() *Registry {
	return &Registry{commands: map[string]Command{}}
}

func Register(r *Registry, appName, version, build string) {
	r.appName = appName
	r.version = version
	r.build = build
	r.Add(&ServeCommand{})
}

func (r *Registry) Add(cmd Command) {
	r.commands[cmd.Name()] = cmd
}

func (r *Registry) Execute(args []string) error {
	if len(args) == 0 {
		return r.commands["serve"].Execute(nil)
	}
	switch args[0] {
	case "help", "-h", "--help":
		r.PrintHelp()
		return nil
	case "--version", "version":
		fmt.Printf("%s %s (%s)\n", r.appName, r.version, r.build)
		return nil
	}
	cmd, ok := r.commands[args[0]]
	if !ok {
		r.PrintHelp()
		return fmt.Errorf("unknown command %q", args[0])
	}
	return cmd.Execute(args[1:])
}

func (r *Registry) PrintHelp() {
	fmt.Printf("%s %s\n\n", r.appName, r.version)
	fmt.Println("Commands:")
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cmd := r.commands[name]
		fmt.Printf("  %-10s %s\n", cmd.Name(), cmd.Description())
	}
	fmt.Println("\nExamples:")
	fmt.Println("  go run . serve")
}

func parseKVArgs(args []string) map[string]string {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		key := args[i]
		if len(key) < 3 || key[:2] != "--" {
			continue
		}
		key = key[2:]
		if before, after, ok := strings.Cut(key, "="); ok {
			values[before] = after
			continue
		}
		if i+1 < len(args) && (len(args[i+1]) < 2 || args[i+1][:2] != "--") {
			values[key] = args[i+1]
			i++
			continue
		}
		values[key] = "true"
	}
	return values
}
