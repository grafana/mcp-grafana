package testpkg

import "github.com/grafana/grafana-openapi-client-go/client/datasources"

func badCalls(c datasources.ClientService) {
	c.GetDataSources()            // want "use GetDataSourcesWithParams with a context-aware params object instead of GetDataSources which drops request context"
	c.GetDataSourceByUID("myuid") // want "use GetDataSourceByUIDWithParams with a context-aware params object instead of GetDataSourceByUID which drops request context"
}

func goodCalls(c datasources.ClientService) {
	c.GetDataSourcesWithParams(nil)
	c.GetDataSourceByUIDWithParams(nil)
	c.SomeHelperMethod() // no WithParams variant, should not be flagged
}
