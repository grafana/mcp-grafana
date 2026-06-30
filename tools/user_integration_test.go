//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserInfo_Integration verifies user_info against the docker-compose
// Grafana: the admin identity belongs to the default org and the secondary org
// seeded by the orgs-seed job, so it should report both plus a usage note.
func TestUserInfo_Integration(t *testing.T) {
	ctx := newTestContext()

	res, err := getUserInfo(ctx, UserInfoParams{})
	require.NoError(t, err)

	assert.NotEmpty(t, res.Login, "should report the signed-in login")
	assert.Greater(t, res.CurrentOrgID, int64(0), "should report a current org")

	orgIDs := map[int64]bool{}
	for _, o := range res.Orgs {
		orgIDs[o.OrgID] = true
	}
	assert.Truef(t, orgIDs[1], "admin should be a member of org 1; orgs=%+v", res.Orgs)
	assert.Truef(t, orgIDs[2], "admin should be a member of the seeded org 2; orgs=%+v", res.Orgs)

	// With more than one accessible org, a usage note explaining org selection
	// is included.
	assert.NotEmpty(t, res.Usage)
}
