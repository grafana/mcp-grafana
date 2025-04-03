package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

type InvestigationStatus string

const (
	InvestigationStatusPending  InvestigationStatus = "pending"
	InvestigationStatusRunning  InvestigationStatus = "running"
	InvestigationStatusFinished InvestigationStatus = "finished"
	InvestigationStatusFailed   InvestigationStatus = "failed"
)

type AnalysisStatus string

const (
	AnalysisStatusPending       AnalysisStatus = "pending"
	AnalysisStatusSkipped       AnalysisStatus = "skipped"
	AnalysisStatusRunning       AnalysisStatus = "running"
	AnalysisStatusFinished      AnalysisStatus = "finished"
	AnalysisRunningStuckMessage string         = "Analysis was stuck in a running state for too long."
	AnalysisPendingStuckMessage string         = "Analysis was stuck in a pending state for too long."
)

type InvestigationRequest struct {
	AlertLabels map[string]string `json:"alertLabels,omitempty" gorm:"-"`
	Labels      map[string]string `json:"labels"`

	Start time.Time `json:"start" gorm:"not null"`
	End   time.Time `json:"end" gorm:"not null"`

	// TODO: Add this when we have a new investigation source field InvestigationSourceTypeMCP.
	// InvestigationSource InvestigationSource `json:"investigationSource" gorm:"embedded;embeddedPrefix:investigation_source_"`

	// This field holds metric query input for oncall integration investigations.
	// To be removed after migrating to a new more explicit field.
	QueryURL string `json:"queryUrl"`

	Checks []string `json:"checks" gorm:"serializer:json"`
}

// AnalysisStep represents a single step in the analysis process.
type AnalysisStep struct {
	CreatedAt time.Time `json:"created" validate:"isdefault"`
	// State that the Analysis is entering.
	State string `json:"state"`
	// The exit message of the step. Can be empty if the step was successful.
	ExitMessage string `json:"exitMessage"`
	// Runtime statistics for this step, stored as JSON in the database
	Stats map[string]interface{} `json:"stats,omitempty"`
}

type AnalysisEvent struct {
	StartTime   time.Time              `json:"startTime"`
	EndTime     time.Time              `json:"endTime"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Details     map[string]interface{} `json:"details"`
}

// Interesting: The analysis complete with results that indicate a probable cause for failure.
type AnalysisResult struct {
	Successful  bool   `json:"successful"`
	Interesting bool   `json:"interesting"`
	Message     string `json:"message"`
	// Do not put these into the database while we are testing it out. Just used for sending to OnCall.
	MarkdownSummary string                 `json:"-" gorm:"-"`
	Details         map[string]interface{} `json:"details"`
	Events          []AnalysisEvent        `json:"events,omitempty" gorm:"serializer:json"`
}

// An Analysis struct provides the status and results
// of running a specific type of check.
// The information is stored in the database.
type Analysis struct {
	ID        uuid.UUID `json:"id" gorm:"primarykey;type:char(36)" validate:"isdefault"`
	CreatedAt time.Time `json:"created" validate:"isdefault"`
	UpdatedAt time.Time `json:"modified" validate:"isdefault"`

	Status    AnalysisStatus `json:"status" gorm:"default:pending;index:idx_analyses_stats,priority:100"`
	StartedAt *time.Time     `json:"started" validate:"isdefault"`

	// Foreign key to the Investigation that created this Analysis.
	InvestigationID uuid.UUID `json:"investigationId" gorm:"index:idx_analyses_stats,priority:10"`

	// Name is the name of the check that this analysis represents.
	Name   string         `json:"name"`
	Title  string         `json:"title"`
	Steps  []AnalysisStep `json:"steps" gorm:"foreignKey:AnalysisID;constraint:OnDelete:CASCADE"`
	Result AnalysisResult `json:"result" gorm:"embedded;embeddedPrefix:result_"`

	// CreateWithID is used to indicate that the analysis should be created with the
	// ID in the ID field, rather than generating a new one. This is used by the
	// copy command of the mlapi CLI.
	CreateWithID bool `json:"-" gorm:"-"`
}

type DatasourceConfig struct {
	LokiDatasource       DatasourceInfo `json:"lokiDatasource" gorm:"not null;embedded;embeddedPrefix:loki_"`
	PrometheusDatasource DatasourceInfo `json:"prometheusDatasource" gorm:"not null;embedded;embeddedPrefix:prometheus_"`
	TempoDatasource      DatasourceInfo `json:"tempoDatasource" gorm:"not null;embedded;embeddedPrefix:tempo_"`
	PyroscopeDatasource  DatasourceInfo `json:"pyroscopeDatasource" gorm:"not null;embedded;embeddedPrefix:pyroscope_"`
}

type DatasourceInfo struct {
	Uid string `json:"uid"`
}

// AnalysisMeta represents metadata about the analyses
type AnalysisMeta struct {
	CountsByStage map[string]interface{} `json:"countsByStage"`
	Items         []Analysis             `json:"items"`
}

type Investigation struct {
	ID        uuid.UUID `json:"id" gorm:"primarykey;type:char(36)" validate:"isdefault"`
	CreatedAt time.Time `json:"created" gorm:"index" validate:"isdefault"`
	UpdatedAt time.Time `json:"modified" validate:"isdefault"`

	TenantID string `json:"tenantId" gorm:"index;not null;size:256"`

	Datasources DatasourceConfig `json:"datasources" gorm:"embedded;embeddedPrefix:datasources_"`

	Name        string               `json:"name"`
	RequestData InvestigationRequest `json:"requestData" gorm:"not null;embedded;embeddedPrefix:request_"`

	// TODO: Add this when we want to extract discovered inputs for later usage
	// Inputs      Inputs               `json:"inputs" gorm:"serializer:json"`

	// GrafanaURL is the Grafana URL to be used for datasource queries
	// for this investigation.
	// If missing from a request then the `X-Grafana-URL` header is used.
	GrafanaURL string `json:"grafanaUrl"`

	// Status describes the state of the investigation (pending, running, failed, or finished).
	// This is stored in the db along with the failure reason if the investigation failed.
	// It is computed in the AfterUpdate hook on Analyses.
	//
	// Note this is not tagged as validate:"isdefault" because we want users to be able
	// to take an existing investigation, update a field or two, and just POST it to
	// create a new investigation. If we included that tag we'd return a 400 here even
	// though we just ignore the field, which just adds a slightly annoying step for users.
	Status InvestigationStatus `json:"status"`

	// FailureReason is a short human-friendly string that explains the reason that the
	// investigation failed.
	FailureReason string `json:"failureReason,omitempty"`

	// Analyses contains metadata about the investigation's analyses
	Analyses AnalysisMeta `json:"analyses"`
}

type RequestData struct {
	Labels map[string]string `json:"labels"`
	Checks []string          `json:"checks"`
}

// SiftClient represents a client for interacting with the Sift API
type SiftClient struct {
	client *http.Client
	url    string
}

func NewSiftClient(url, apiKey string) *SiftClient {
	client := &http.Client{
		Transport: &authRoundTripper{
			apiKey:     apiKey,
			underlying: http.DefaultTransport,
		},
	}
	return &SiftClient{
		client: client,
		url:    url,
	}
}

func siftClientFromContext(ctx context.Context) (*SiftClient, error) {
	// Get the standard Grafana URL and API key
	grafanaURL, grafanaAPIKey := mcpgrafana.GrafanaURLFromContext(ctx), mcpgrafana.GrafanaAPIKeyFromContext(ctx)

	client := NewSiftClient(grafanaURL, grafanaAPIKey)

	return client, nil
}

// CheckType represents the type of analysis check to perform
type CheckType string

const (
	CheckTypeErrorPatternLogs CheckType = "ErrorPatternLogs"
	CheckTypeSlowRequests     CheckType = "SlowRequests"
)

// CreateInvestigationParams defines the parameters for creating a new investigation
type CreateInvestigationParams struct {
	Name        string               `json:"name" jsonschema:"required,description=The name of the investigation"`
	RequestData InvestigationRequest `json:"requestData" jsonschema:"required,description=The request data for the investigation"`
	Verbose     bool                 `json:"verbose,omitempty" jsonschema:"description=Whether to include extra details in the results of each analysis"`
	Checks      []CheckType          `json:"checks,omitempty" jsonschema:"description=Optional list of specific checks to run. Can include ErrorPatternLogs and SlowRequests. If not provided, all checks will be run."`
}

// createInvestigation creates a new investigation
func createInvestigation(ctx context.Context, args CreateInvestigationParams) (*Investigation, error) {
	client, err := siftClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Sift client: %w", err)
	}

	// Set default time range to last 30 minutes if not provided
	if args.RequestData.Start.IsZero() {
		args.RequestData.Start = time.Now().Add(-30 * time.Minute)
	}
	if args.RequestData.End.IsZero() {
		args.RequestData.End = time.Now()
	}

	// If checks are provided, validate them
	if len(args.Checks) > 0 {
		for _, check := range args.Checks {
			switch check {
			case CheckTypeErrorPatternLogs, CheckTypeSlowRequests:
			default:
				return nil, fmt.Errorf("invalid check type: %s. Valid types are: %s, %s",
					check, CheckTypeErrorPatternLogs, CheckTypeSlowRequests)
			}
		}
	}

	investigation := &Investigation{
		Name:        args.Name,
		RequestData: args.RequestData,
		GrafanaURL:  client.url,
		Status:      InvestigationStatusPending,
	}

	return client.createInvestigation(ctx, investigation)
}

// CreateInvestigation is a tool for creating new investigations
var CreateInvestigation = mcpgrafana.MustTool(
	"create_investigation",
	"Create a new investigation. An investigation analyzes data from different datasource types. It takes a set of labels and values to scope the analysis, optionally accepts a time range (defaults to last hour if not specified) and the title can be infered by the labels used. The investigation will automatically explore relevant data sources and provide insights about potential causes. Optionally pass ErrorPatternLogs or SlowRequests as check types to run specific checks.",
	createInvestigation,
)

// GetInvestigationParams defines the parameters for retrieving an investigation
type GetInvestigationParams struct {
	ID string `json:"id" jsonschema:"required,description=The UUID of the investigation as a string (e.g. '02adab7c-bf5b-45f2-9459-d71a2c29e11b')"`
}

// getInvestigation retrieves an existing investigation
func getInvestigation(ctx context.Context, args GetInvestigationParams) (*Investigation, error) {
	client, err := siftClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Sift client: %w", err)
	}

	// Parse the UUID string
	id, err := uuid.Parse(args.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid investigation ID format: %w", err)
	}

	investigation, err := client.getInvestigation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting investigation: %w", err)
	}

	return investigation, nil
}

// GetInvestigation is a tool for retrieving an existing investigation
var GetInvestigation = mcpgrafana.MustTool(
	"get_investigation",
	"Retrieves an existing investigation by its UUID. The ID should be provided as a string in UUID format (e.g. '02adab7c-bf5b-45f2-9459-d71a2c29e11b').",
	getInvestigation,
)

// GetAnalysisParams defines the parameters for retrieving a specific analysis
type GetAnalysisParams struct {
	InvestigationID string `json:"investigationId" jsonschema:"required,description=The UUID of the investigation as a string (e.g. '02adab7c-bf5b-45f2-9459-d71a2c29e11b')"`
	AnalysisID      string `json:"analysisId" jsonschema:"required,description=The UUID of the specific analysis to retrieve"`
}

// getAnalysis retrieves a specific analysis from an investigation
func getAnalysis(ctx context.Context, args GetAnalysisParams) (*Analysis, error) {
	client, err := siftClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Sift client: %w", err)
	}

	// Parse the UUID strings
	investigationID, err := uuid.Parse(args.InvestigationID)
	if err != nil {
		return nil, fmt.Errorf("invalid investigation ID format: %w", err)
	}

	analysisID, err := uuid.Parse(args.AnalysisID)
	if err != nil {
		return nil, fmt.Errorf("invalid analysis ID format: %w", err)
	}

	analysis, err := client.getAnalysis(ctx, investigationID, analysisID)
	if err != nil {
		return nil, fmt.Errorf("getting analysis: %w", err)
	}

	return analysis, nil
}

// GetAnalysis is a tool for retrieving a specific analysis from an investigation
var GetAnalysis = mcpgrafana.MustTool(
	"get_analysis",
	"Retrieves a specific analysis from an investigation by their UUIDs. Both the investigation ID and analysis ID should be provided as strings in UUID format.",
	getAnalysis,
)

// ListInvestigationsParams defines the parameters for retrieving investigations
type ListInvestigationsParams struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum number of investigations to return. Defaults to 10 if not specified."`
}

// listInvestigations retrieves a list of investigations with an optional limit
func listInvestigations(ctx context.Context, args ListInvestigationsParams) ([]Investigation, error) {
	client, err := siftClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Sift client: %w", err)
	}

	// Set default limit if not provided
	if args.Limit <= 0 {
		args.Limit = 10
	}

	investigations, err := client.listInvestigations(ctx, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("getting investigations: %w", err)
	}

	return investigations, nil
}

// ListInvestigations is a tool for retrieving a list of investigations
var ListInvestigations = mcpgrafana.MustTool(
	"list_investigations",
	"Retrieves a list of investigations with an optional limit. If no limit is specified, defaults to 10 investigations.",
	listInvestigations,
)

// AddSiftTools registers all Sift tools with the MCP server
func AddSiftTools(mcp *server.MCPServer) {
	CreateInvestigation.Register(mcp)
	GetInvestigation.Register(mcp)
	GetAnalysis.Register(mcp)
	ListInvestigations.Register(mcp)
}

// makeRequest is a helper method to make HTTP requests and handle common response patterns
func (c *SiftClient) makeRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.url+path, bytes.NewBuffer(body))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.url+path, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
	}

	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	reader := io.LimitReader(response.Body, 1024*1024*48)
	defer response.Body.Close()

	buf, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return buf, nil
}

// getInvestigation is a helper method to get the current status of an investigation
func (c *SiftClient) getInvestigation(ctx context.Context, id uuid.UUID) (*Investigation, error) {
	buf, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/%s", id), nil)
	if err != nil {
		return nil, err
	}

	investigationResponse := struct {
		Status string        `json:"status"`
		Data   Investigation `json:"data"`
	}{}

	if err := json.Unmarshal(buf, &investigationResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w. body: %s", err, buf)
	}

	return &investigationResponse.Data, nil
}

func (c *SiftClient) createInvestigation(ctx context.Context, investigation *Investigation) (*Investigation, error) {
	jsonData, err := json.Marshal(investigation)
	if err != nil {
		return nil, fmt.Errorf("marshaling investigation: %w", err)
	}

	buf, err := c.makeRequest(ctx, "POST", "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations", jsonData)
	if err != nil {
		return nil, err
	}

	investigationResponse := struct {
		Status string        `json:"status"`
		Data   Investigation `json:"data"`
	}{}

	if err := json.Unmarshal(buf, &investigationResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w. body: %s", err, buf)
	}

	// Poll for investigation completion
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for investigation completion")
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for investigation completion after 5 minutes")
		case <-ticker.C:
			investigation, err := c.getInvestigation(ctx, investigationResponse.Data.ID)
			if err != nil {
				return nil, err
			}

			if investigation.Status == InvestigationStatusFailed {
				return nil, fmt.Errorf("investigation failed: %s", investigation.FailureReason)
			}

			if investigation.Status == InvestigationStatusFinished {
				return investigation, nil
			}
		}
	}
}

// getAnalyses is a helper method to get all analyses from an investigation
func (c *SiftClient) getAnalyses(ctx context.Context, investigationID uuid.UUID) ([]Analysis, error) {
	path := fmt.Sprintf("/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/%s/analyses", investigationID)
	buf, err := c.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}

	var response struct {
		Status string     `json:"status"`
		Data   []Analysis `json:"data"`
	}

	if err := json.Unmarshal(buf, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w. body: %s", err, buf)
	}

	return response.Data, nil
}

// getAnalysis is a helper method to get a specific analysis from an investigation
func (c *SiftClient) getAnalysis(ctx context.Context, investigationID, analysisID uuid.UUID) (*Analysis, error) {
	// First get all analyses to verify the analysis exists
	analyses, err := c.getAnalyses(ctx, investigationID)
	if err != nil {
		return nil, fmt.Errorf("getting analyses: %w", err)
	}

	// Find the specific analysis
	var targetAnalysis *Analysis
	for _, analysis := range analyses {
		if analysis.ID == analysisID {
			targetAnalysis = &analysis
			break
		}
	}

	if targetAnalysis == nil {
		return nil, fmt.Errorf("analysis with ID %s not found in investigation %s", analysisID, investigationID)
	}

	return targetAnalysis, nil
}

// listInvestigations is a helper method to get a list of investigations
func (c *SiftClient) listInvestigations(ctx context.Context, limit int) ([]Investigation, error) {
	path := fmt.Sprintf("/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations?limit=%d", limit)
	buf, err := c.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}

	var response struct {
		Status string          `json:"status"`
		Data   []Investigation `json:"data"`
	}

	if err := json.Unmarshal(buf, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w. body: %s", err, buf)
	}

	return response.Data, nil
}
