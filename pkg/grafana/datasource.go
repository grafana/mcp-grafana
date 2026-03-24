// package grafana
package grafana

import "errors"

var ErrNoRows = errors.New("no rows in result set")

type DSQueryPayload struct {
	Queries []any  `json:"queries"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type DsQueryFrameField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	TypeInfo struct {
		Frame string `json:"frame,omitempty"`
	} `json:"typeInfo,omitempty"`
	Labels map[string]string      `json:"labels"`
	Config map[string]interface{} `json:"config,omitempty"`
}

type DsQueryFrameSchema struct {
	Name   string              `json:"name,omitempty"`
	RefID  string              `json:"refId,omitempty"`
	Fields []DsQueryFrameField `json:"fields"`
}

type DSQueryFrameData struct {
	Values [][]interface{} `json:"values"`
}

type DsQueryFrame struct {
	Schema DsQueryFrameSchema `json:"schema,omitempty"`
	Data   DSQueryFrameData   `json:"data"`
}

type DsQueryResult struct {
	Status int            `json:"status,omitempty"`
	Frames []DsQueryFrame `json:"frames,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// DSQueryResponse represents the raw API response from Grafana's /api/ds/query
type DSQueryResponse struct {
	Results map[string]DsQueryResult `json:"results"`
}
