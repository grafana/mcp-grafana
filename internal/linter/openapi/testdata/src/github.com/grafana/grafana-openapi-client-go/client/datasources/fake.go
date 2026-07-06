package datasources

type GetDataSourcesParams struct{}
type GetDataSourcesOK struct{}
type GetDataSourceByUIDParams struct{}
type GetDataSourceByUIDOK struct{}
type ClientOption func()

type ClientService interface {
	GetDataSources(opts ...ClientOption) (*GetDataSourcesOK, error)
	GetDataSourcesWithParams(params *GetDataSourcesParams, opts ...ClientOption) (*GetDataSourcesOK, error)
	GetDataSourceByUID(uid string, opts ...ClientOption) (*GetDataSourceByUIDOK, error)
	GetDataSourceByUIDWithParams(params *GetDataSourceByUIDParams, opts ...ClientOption) (*GetDataSourceByUIDOK, error)
	// A method with no WithParams variant should not be flagged.
	SomeHelperMethod() error
}
