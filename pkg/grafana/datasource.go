package grafana

import "errors"

var ErrNoRows = errors.New("no rows in result set")

type DSQueryPayload struct {
	Queries []any  `json:"queries"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type DSQueryFrameField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	TypeInfo struct {
		Frame string `json:"frame,omitempty"`
	} `json:"typeInfo,omitempty"`
	Labels map[string]string      `json:"labels"`
	Config map[string]interface{} `json:"config,omitempty"`
}

type DSQueryFrameSchema struct {
	Name   string              `json:"name,omitempty"`
	RefID  string              `json:"refId,omitempty"`
	Fields []DSQueryFrameField `json:"fields"`
}

type DSQueryFrameData struct {
	Values [][]interface{} `json:"values"`
}

type DSQueryFrame struct {
	Schema DSQueryFrameSchema `json:"schema,omitempty"`
	Data   DSQueryFrameData   `json:"data"`
}

type DSQueryResult struct {
	Status int            `json:"status,omitempty"`
	Frames []DSQueryFrame `json:"frames,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// DSQueryResponse represents the raw API response from Grafana's /api/ds/query
type DSQueryResponse struct {
	Results map[string]DSQueryResult `json:"results"`
}
