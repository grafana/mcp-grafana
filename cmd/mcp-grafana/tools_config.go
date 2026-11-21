package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/grafana/mcp-grafana/v2/tools"
)

const configEnvVar = "GRAFANA_MCP_CONFIG"

// configFile is the file --config points to. tools is its only section.
type configFile struct {
	Tools toolsConfig `yaml:"tools"`
}

// toolsConfig selects tools the same way the tool flags do. A nil field was
// left out of the file and keeps the default.
type toolsConfig struct {
	// Categories is the exact list of enabled categories, like
	// --enabled-tools.
	Categories *[]string `yaml:"categories"`
	Mode       toolsMode `yaml:"mode"`
	// Query false removes the query tools, like --disable-query. Allow does
	// not bring them back.
	Query *bool    `yaml:"query"`
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type toolsMode string

const (
	modeUnset     toolsMode = ""
	modeReadWrite toolsMode = "read-write"
	modeReadOnly  toolsMode = "read-only"
)

func (m *toolsMode) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	switch toolsMode(s) {
	case modeReadWrite, modeReadOnly:
		*m = toolsMode(s)
		return nil
	}
	return fmt.Errorf("line %d: mode must be %q or %q, got %q", node.Line, modeReadWrite, modeReadOnly, s)
}

func loadConfigFile(path string) (configFile, error) {
	var cfg configFile
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("%s: only one YAML document is allowed", path)
	}
	if err := cfg.Tools.policy().Validate(); err != nil {
		return cfg, fmt.Errorf("%s: tools: %w", path, err)
	}
	return cfg, nil
}

// policy is the selection the file makes on its own, before any flag.
func (tc toolsConfig) policy() tools.Policy {
	p := tools.Policy{
		Categories: strings.Split(defaultEnabledTools, ","),
		ReadOnly:   tc.Mode == modeReadOnly,
		NoQuery:    tc.Query != nil && !*tc.Query,
		Allow:      tc.Allow,
		Deny:       tc.Deny,
	}
	if tc.Categories != nil {
		p.Categories = *tc.Categories
	}
	return p
}

// applyConfig layers the file under the tool flags. A flag in set wins over
// the file for the setting it maps to. --enable-write-tools and
// --enable-query add to the file's allow list and take their tools out of
// its deny list.
func (dt *disabledTools) applyConfig(tc toolsConfig, set map[string]bool) {
	if tc.Categories != nil && !set["enabled-tools"] {
		dt.enabledTools = strings.Join(*tc.Categories, ",")
	}
	if tc.Mode != modeUnset && !set["disable-write"] {
		dt.write = tc.Mode == modeReadOnly
	}
	if tc.Query != nil && !set["disable-query"] {
		dt.query = !*tc.Query
	}
	dt.fileAllow = tc.Allow
	dt.fileDeny = tc.Deny
}

func (dt *disabledTools) deny() []string {
	allowed := dt.flagAllow()
	return slices.DeleteFunc(slices.Clone(dt.fileDeny), func(name string) bool {
		return slices.Contains(allowed, name)
	})
}
