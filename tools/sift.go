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
	AlertLabels map[string]string `json:"alertLabels,omitempty"`
	Labels      map[string]string `json:"labels"`

	Start time.Time `json:"start"`
	End   time.Time `json:"end"`

	QueryURL string `json:"queryUrl"`

	Checks []string `json:"checks"`
}

// Interesting: The analysis complete with results that indicate a probable cause for failure.
type AnalysisResult struct {
	Successful  bool                   `json:"successful"`
	Interesting bool                   `json:"interesting"`
	Message     string                 `json:"message"`
	Details     map[string]interface{} `json:"details"`
}

type AnalysisMeta struct {
	Items []Analysis `json:"items"`
}

// An Analysis struct provides the status and results
// of running a specific type of check.
type Analysis struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created"`
	UpdatedAt time.Time `json:"modified"`

	Status    AnalysisStatus `json:"status"`
	StartedAt *time.Time     `json:"started"`

	// Foreign key to the Investigation that created this Analysis.
	InvestigationID uuid.UUID `json:"investigationId"`

	// Name is the name of the check that this analysis represents.
	Name   string         `json:"name"`
	Title  string         `json:"title"`
	Result AnalysisResult `json:"result"`
}

type Investigation struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created"`
	UpdatedAt time.Time `json:"modified"`

	TenantID string `json:"tenantId"`

	Name string `json:"name"`

	// GrafanaURL is the Grafana URL to be used for datasource queries
	// for this investigation.
	GrafanaURL string `json:"grafanaUrl"`

	// Status describes the state of the investigation (pending, running, failed, or finished).
	Status InvestigationStatus `json:"status"`

	// FailureReason is a short human-friendly string that explains the reason that the
	// investigation failed.
	FailureReason string `json:"failureReason,omitempty"`

	Analyses AnalysisMeta `json:"analyses"`
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
	"Retrieves an existing Sift investigation by its UUID. The ID should be provided as a string in UUID format (e.g. '02adab7c-bf5b-45f2-9459-d71a2c29e11b').",
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
	"Retrieves a specific analysis from an investigation by its UUID. The investigation ID and analysis ID should be provided as strings in UUID format.",
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
	"Retrieves a list of Sift investigations with an optional limit. If no limit is specified, defaults to 10 investigations.",
	listInvestigations,
)

// FindErrorPatternLogsParams defines the parameters for running an ErrorPatternLogs check
type FindErrorPatternLogsParams struct {
	Name     string            `json:"name" jsonschema:"required,description=The name of the investigation"`
	Labels   map[string]string `json:"labels" jsonschema:"required,description=Labels to scope the analysis"`
	Start    time.Time         `json:"start,omitempty" jsonschema:"description=Start time for the investigation. Defaults to 30 minutes ago if not specified."`
	End      time.Time         `json:"end,omitempty" jsonschema:"description=End time for the investigation. Defaults to now if not specified."`
	QueryURL string            `json:"queryUrl,omitempty" jsonschema:"description=Optional query URL for the investigation"`
}

// findErrorPatternLogs creates an investigation with ErrorPatternLogs check, waits for it to complete, and returns the analysis
func findErrorPatternLogs(ctx context.Context, args FindErrorPatternLogsParams) (*Analysis, error) {
	client, err := siftClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Sift client: %w", err)
	}

	// Create the investigation request with ErrorPatternLogs check
	requestData := InvestigationRequest{
		Labels:   args.Labels,
		Start:    args.Start,
		End:      args.End,
		QueryURL: args.QueryURL,
		Checks:   []string{string(CheckTypeErrorPatternLogs)},
	}

	investigation := &Investigation{
		Name:       args.Name,
		GrafanaURL: client.url,
		Status:     InvestigationStatusPending,
	}

	// Create the investigation and wait for it to complete
	completedInvestigation, err := client.createInvestigation(ctx, investigation, requestData)
	if err != nil {
		return nil, fmt.Errorf("creating investigation: %w", err)
	}

	// Get all analyses from the completed investigation
	analyses, err := client.getAnalyses(ctx, completedInvestigation.ID)
	if err != nil {
		return nil, fmt.Errorf("getting analyses: %w", err)
	}

	// Find the ErrorPatternLogs analysis
	var errorPatternLogsAnalysis *Analysis
	for i := range analyses {
		if analyses[i].Name == string(CheckTypeErrorPatternLogs) {
			errorPatternLogsAnalysis = &analyses[i]
			break
		}
	}

	if errorPatternLogsAnalysis == nil {
		return nil, fmt.Errorf("ErrorPatternLogs analysis not found in investigation %s", completedInvestigation.ID)
	}

	return errorPatternLogsAnalysis, nil
}

// FindErrorPatternLogs is a tool for running an ErrorPatternLogs check
var FindErrorPatternLogs = mcpgrafana.MustTool(
	"find_error_pattern_logs",
	"Creates an investigation to search for error patterns in logs, waits for it to complete, and returns the analysis results. This tool triggers an investigation in the relevant Loki datasource to determine if there are elevated error rates compared to the last day's average, and returns the error pattern found, if any.",
	findErrorPatternLogs,
)

// FindSlowRequestsParams defines the parameters for running an SlowRequests check
type FindSlowRequestsParams struct {
	Name     string            `json:"name" jsonschema:"required,description=The name of the investigation"`
	Labels   map[string]string `json:"labels" jsonschema:"required,description=Labels to scope the analysis"`
	Start    time.Time         `json:"start,omitempty" jsonschema:"description=Start time for the investigation. Defaults to 30 minutes ago if not specified."`
	End      time.Time         `json:"end,omitempty" jsonschema:"description=End time for the investigation. Defaults to now if not specified."`
	QueryURL string            `json:"queryUrl,omitempty" jsonschema:"description=Optional query URL for the investigation"`
}

// findSlowRequests creates an investigation with SlowRequests check, waits for it to complete, and returns the analysis
func findSlowRequests(ctx context.Context, args FindSlowRequestsParams) (*Analysis, error) {
	client, err := siftClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Sift client: %w", err)
	}

	// Create the investigation request with SlowRequests check
	requestData := InvestigationRequest{
		Labels:   args.Labels,
		Start:    args.Start,
		End:      args.End,
		QueryURL: args.QueryURL,
		Checks:   []string{string(CheckTypeSlowRequests)},
	}

	investigation := &Investigation{
		Name:       args.Name,
		GrafanaURL: client.url,
		Status:     InvestigationStatusPending,
	}

	// Create the investigation and wait for it to complete
	completedInvestigation, err := client.createInvestigation(ctx, investigation, requestData)
	if err != nil {
		return nil, fmt.Errorf("creating investigation: %w", err)
	}

	// Get all analyses from the completed investigation
	analyses, err := client.getAnalyses(ctx, completedInvestigation.ID)
	if err != nil {
		return nil, fmt.Errorf("getting analyses: %w", err)
	}

	// Find the SlowRequests analysis
	var slowRequestsAnalysis *Analysis
	for i := range analyses {
		if analyses[i].Name == string(CheckTypeSlowRequests) {
			slowRequestsAnalysis = &analyses[i]
			break
		}
	}

	if slowRequestsAnalysis == nil {
		return nil, fmt.Errorf("SlowRequests analysis not found in investigation %s", completedInvestigation.ID)
	}

	return slowRequestsAnalysis, nil
}

// FindSlowRequests is a tool for running an SlowRequests check
var FindSlowRequests = mcpgrafana.MustTool(
	"find_slow_requests",
	"Creates an investigation to search for slow requests in the relevant Tempo datasources, waits for it to complete, and returns the analysis results.",
	findSlowRequests,
)

// AddSiftTools registers all Sift tools with the MCP server
func AddSiftTools(mcp *server.MCPServer) {
	GetInvestigation.Register(mcp)
	GetAnalysis.Register(mcp)
	ListInvestigations.Register(mcp)
	FindErrorPatternLogs.Register(mcp)
	FindSlowRequests.Register(mcp)
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

func (c *SiftClient) createInvestigation(ctx context.Context, investigation *Investigation, requestData InvestigationRequest) (*Investigation, error) {
	// Set default time range to last 30 minutes if not provided
	if requestData.Start.IsZero() {
		requestData.Start = time.Now().Add(-30 * time.Minute)
	}
	if requestData.End.IsZero() {
		requestData.End = time.Now()
	}

	// Create the payload including the necessary fields for the API
	payload := struct {
		Investigation
		RequestData InvestigationRequest `json:"requestData"`
	}{
		Investigation: *investigation,
		RequestData:   requestData,
	}

	jsonData, err := json.Marshal(payload)
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
