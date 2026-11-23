//go:build unit

package tools

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadOnlyDescriptionsDoNotAdvertiseWrites(t *testing.T) {
	var names []string
	full := map[string]string{}
	for _, c := range catalog {
		names = append(names, c.name)
		full[c.name] = c.description
	}
	writes := regexp.MustCompile(`(?i)\b(create|update|delete|manage|resolve)\b`)
	for _, c := range Resolve(Policy{Categories: names, ReadOnly: true}).Categories {
		if c.Description != full[c.Name] {
			continue // reduced
		}
		assert.False(t, writes.MatchString(c.Description), "category %q is unreduced under ReadOnly but advertises writes: %s", c.Name, c.Description)
	}
}
