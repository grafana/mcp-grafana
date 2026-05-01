//go:build unit
// +build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertGroupActionForState(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		wantAction string
		wantErr    bool
	}{
		{
			name:       "acknowledged",
			state:      "acknowledged",
			wantAction: "acknowledge",
		},
		{
			name:       "unacknowledged",
			state:      "unacknowledged",
			wantAction: "unacknowledge",
		},
		{
			name:       "resolved",
			state:      "resolved",
			wantAction: "resolve",
		},
		{
			name:       "unresolved",
			state:      "unresolved",
			wantAction: "unresolve",
		},
		{
			name:       "normalizes case and whitespace",
			state:      " Resolved ",
			wantAction: "resolve",
		},
		{
			name:    "rejects unsupported state",
			state:   "silenced",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, err := alertGroupActionForState(tt.state)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantAction, action)
		})
	}
}
