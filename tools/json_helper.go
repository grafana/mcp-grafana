package tools

import (
	"encoding/json"
	"fmt"
)

// appends memory limit exceeded hint message when encounters an errors.
// Uses json.Unmarshal to parse data
func UnmarshalWithLimitMsg(data []byte, v any, bytesLimit int) error {
	if err := json.Unmarshal(data, v); err != nil {
		extraInfo := ""
		if len(data) >= int(bytesLimit) {
			extraInfo = "response size exceeds max memory limit , try with reduced query limits"
		}
		return fmt.Errorf("unmarshaling response: %w; %s", err, extraInfo)
	}
	return nil
}
