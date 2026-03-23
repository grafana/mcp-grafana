package tools

import (
	"encoding/json"
	"fmt"
)

// unmarshalJSONWithLimitMsg unmarshals JSON data into v
// if unmarshal fails due to exceeding limit
// it returns a relavant error message.
func unmarshalJSONWithLimitMsg(data []byte, v any, bytesLimit int) error {
	if err := json.Unmarshal(data, v); err != nil {
		extraInfo := ""
		if len(data) >= int(bytesLimit) {
			extraInfo = "response size exceeds limit, try querying in segments"
		}
		return fmt.Errorf("unmarshaling response: %w %s", err, extraInfo)
	}
	return nil
}
