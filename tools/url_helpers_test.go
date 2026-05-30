package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildURL(t *testing.T) {
	base := "http://proxy/api/datasources/proxy/uid"

	assert.Equal(t, base+"/_msearch", buildURL(base, "/_msearch"))
	assert.Equal(t, base+"/indexes", buildURL(base, "indexes"))
	assert.Equal(t, base+"/indexes", buildURL(base, "/indexes"))
	assert.Equal(t, base+"/indexes", buildURL(base+"/", "indexes"))
	assert.Equal(t, base+"/indexes", buildURL(base+"/", "/indexes"))
}
