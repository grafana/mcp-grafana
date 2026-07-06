package testpkg

import "github.com/grafana/grafana-openapi-client-go/client/datasources"

func testHelper(c datasources.ClientService) {
	// These convenience calls in test files should NOT be flagged.
	c.GetDataSources()
	c.GetDataSourceByUID("myuid")
}
