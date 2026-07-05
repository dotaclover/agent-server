package llm

import (
	"encoding/json"
	"fmt"
)

func validateToolCallJSONArguments(provider, name, id, raw string) error {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return fmt.Errorf("%s tool call %q (%s) has invalid JSON arguments: %w", provider, name, id, err)
	}
	if args == nil {
		return fmt.Errorf("%s tool call %q (%s) has invalid JSON arguments: arguments must be a JSON object", provider, name, id)
	}
	return nil
}
