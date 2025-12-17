package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePromQL(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{
			name:    "valid simple metric query",
			expr:    "up",
			wantErr: false,
		},
		{
			name:    "valid metric with label matcher",
			expr:    `up{job="prometheus"}`,
			wantErr: false,
		},
		{
			name:    "valid rate query",
			expr:    `rate(http_requests_total[5m])`,
			wantErr: false,
		},
		{
			name:    "valid aggregation query",
			expr:    `sum by(job) (rate(http_requests_total[5m]))`,
			wantErr: false,
		},
		{
			name:    "valid binary operation",
			expr:    `up == 1`,
			wantErr: false,
		},
		{
			name:    "empty expression",
			expr:    "",
			wantErr: true,
		},
		{
			name:    "invalid syntax - unclosed bracket",
			expr:    `rate(http_requests_total[5m]`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - unclosed label matcher",
			expr:    `up{job="prometheus"`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - unknown function",
			expr:    `unknown_function(up)`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - malformed label matcher",
			expr:    `up{job=}`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - unexpected character",
			expr:    `up @`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePromQL(tt.expr)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for expression: %s", tt.expr)
				assert.Contains(t, err.Error(), "PromQL", "Error message should mention PromQL")
			} else {
				require.NoError(t, err, "Expected no error for valid expression: %s", tt.expr)
			}
		})
	}
}

func TestValidateLogQL(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{
			name:    "valid simple log selector",
			expr:    `{app="foo"}`,
			wantErr: false,
		},
		{
			name:    "valid log selector with multiple labels",
			expr:    `{app="foo", env="prod"}`,
			wantErr: false,
		},
		{
			name:    "valid log selector with filter",
			expr:    `{app="foo"} |= "error"`,
			wantErr: false,
		},
		{
			name:    "valid log selector with regex filter",
			expr:    `{app="foo"} |~ "error.*"`,
			wantErr: false,
		},
		{
			name:    "valid log selector with json parser",
			expr:    `{app="foo"} | json`,
			wantErr: false,
		},
		{
			name:    "valid metric query with rate",
			expr:    `rate({app="foo"}[5m])`,
			wantErr: false,
		},
		{
			name:    "valid metric query with count_over_time",
			expr:    `count_over_time({app="foo"}[5m])`,
			wantErr: false,
		},
		{
			name:    "valid metric query with sum aggregation",
			expr:    `sum(rate({app="foo"}[5m])) by (host)`,
			wantErr: false,
		},
		{
			name:    "valid complex pipeline",
			expr:    `{app="foo"} | json | line_format "{{.message}}"`,
			wantErr: false,
		},
		{
			name:    "empty expression",
			expr:    "",
			wantErr: true,
		},
		{
			name:    "invalid syntax - unclosed label matcher",
			expr:    `{app="foo"`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - missing equals in label",
			expr:    `{app}`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - invalid filter operator",
			expr:    `{app="foo"} |* "error"`,
			wantErr: true,
		},
		{
			name:    "invalid syntax - malformed rate",
			expr:    `rate({app="foo"})`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLogQL(tt.expr)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for expression: %s", tt.expr)
				assert.Contains(t, err.Error(), "LogQL", "Error message should mention LogQL")
			} else {
				require.NoError(t, err, "Expected no error for valid expression: %s", tt.expr)
			}
		})
	}
}
