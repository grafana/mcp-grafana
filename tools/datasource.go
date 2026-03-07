package tools

import (
	"sync"
)

type DataSource any

var dataSources sync.Map

func LoadOrStoreDataSource(dataSourceType string, value func() DataSource) DataSource {
	ds, ok := dataSources.Load(dataSourceType)
	if ok {
		return ds
	}
	ds = value()
	dataSources.Store(dataSourceType, ds)
	return ds
}
