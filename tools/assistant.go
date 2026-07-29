// Package tools: assistant.go exposes the `ask_assistant` tool, a thin transport
// wrapper that forwards a natural-language prompt to Grafana Assistant via the
// grafana-assistant-app plugin's A2A (Agent-to-Agent) endpoint and returns the
// aggregated text reply.
//
// All of the assistant "intelligence" (the agent, its prompts, tool
// orchestration, and LLM calls) lives server-side behind the A2A endpoint inside
// the plugin backend. This file only speaks the documented JSON-RPC /
// server-sent-events (SSE) contract, so it carries no private dependency on the
// (internal) grafana-assistant-app module.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// assistantAgentID is the default A2A agent that backs the assistant.
	assistantAgentID = "grafana_assistant_api"

	// assistantAppSource identifies this client for usage/billing attribution.
	// It is sent as the X-App-Source header on every request.
	assistantAppSource = "mcp-grafana"

	// assistantPathPattern is the plugin resource path for the A2A agent
	// endpoint. The single verb is the (URL-escaped) agent ID.
	assistantPathPattern = "/api/plugins/grafana-assistant-app/resources/api/v1/a2a/agents/%s"

	// assistantDefaultTimeout bounds a single ask_assistant call. Complex
	// assistant tasks can take a while, so this is deliberately generous.
	assistantDefaultTimeout = 5 * time.Minute

	// assistantMaxAttempts is the number of times a stream open is attempted
	// before giving up. Only transient failures are retried.
	assistantMaxAttempts = 3

	// assistantInitialBackoff is the base backoff between retry attempts.
	assistantInitialBackoff = 500 * time.Millisecond

	// assistantMaxBackoff caps the exponential backoff between retries.
	assistantMaxBackoff = 5 * time.Second
)

// Sentinel errors surfaced to the caller. Task-level failures are distinct from
// transport failures so callers can tell "the assistant could not answer" from
// "the request never reached the assistant".
var (
	// errAssistantTimeout indicates the HTTP request/stream timed out.
	errAssistantTimeout = errors.New("request to Grafana Assistant timed out")
	// errAssistantTaskFailed indicates the agent task failed server-side.
	errAssistantTaskFailed = errors.New("Grafana Assistant task failed")
	// errAssistantTaskCanceled indicates the agent task was canceled server-side.
	errAssistantTaskCanceled = errors.New("Grafana Assistant task was canceled")
	// errAssistantTaskTimeout indicates the agent task timed out server-side.
	errAssistantTaskTimeout = errors.New("Grafana Assistant task exceeded its server-side timeout")
	// errAssistantIncompleteStream indicates the SSE stream ended before a
	// terminal task state was seen, so any accumulated reply is partial.
	errAssistantIncompleteStream = errors.New("Grafana Assistant stream ended before the task completed")
)

// AskAssistantParams is the input for the ask_assistant tool.
type AskAssistantParams struct {
	Prompt    string `json:"prompt" jsonschema:"required,description=The question or instruction to send to Grafana Assistant (natural language)."`
	ContextID string `json:"contextId,omitempty" jsonschema:"description=Optional context ID from a previous ask_assistant response to continue the same conversation."`
}

// AskAssistantResult is the structured output of the ask_assistant tool.
type AskAssistantResult struct {
	// Response is the assistant's complete text reply.
	Response string `json:"response"`
	// ContextID identifies the conversation; pass it back in a follow-up call
	// to continue the same conversation.
	ContextID string `json:"contextId,omitempty"`
}

const askAssistantDescription = `Send a message to Grafana Assistant and wait for the full text reply. The assistant may use tools, metrics, logs, and other stack context—broader than firing one isolated data-source query.

Use for open-ended questions, triage, or anything that needs assistant reasoning and tool use, in addition to the more targeted MCP tools.

**Multi-turn:** pass contextId from a previous ask_assistant result to continue the same conversation.

**Time:** complex tasks can take several minutes; the call blocks until the reply is done or the request times out. Consider running this tool as a background task if able.`

// askAssistant is the tool handler. It POSTs a JSON-RPC message/stream request
// to the plugin's A2A endpoint and drains the SSE response into a single reply.
func askAssistant(ctx context.Context, args AskAssistantParams) (*AskAssistantResult, error) {
	if strings.TrimSpace(args.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required and must not be empty")
	}

	client, err := newAssistantClient(ctx)
	if err != nil {
		return nil, err
	}

	// Bound the whole call so a stuck stream cannot block forever. Respect any
	// shorter deadline already on the parent context.
	callCtx, cancel := context.WithTimeout(ctx, assistantDefaultTimeout)
	defer cancel()

	prompt := args.Prompt + "\n" + formatAssistantTimeContext(time.Now())
	body, err := buildAssistantRequest(prompt, strings.TrimSpace(args.ContextID))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	endpoint := client.url + fmt.Sprintf(assistantPathPattern, url.PathEscape(assistantAgentID))

	result, err := client.chat(callCtx, endpoint, body)
	if err != nil {
		return result, annotateAssistantError(result, err)
	}
	return result, nil
}

// annotateAssistantError folds any partial reply captured before a failure into
// the error. The shared MCP tool wrapper discards the handler's result value
// whenever the error is non-nil and surfaces only err.Error() to the client, so
// without this the caller would lose both the partial text and the contextId
// needed to resume the conversation. The sentinel is preserved via %w so
// errors.Is still works for callers/tests.
func annotateAssistantError(result *AskAssistantResult, err error) error {
	if err == nil || result == nil {
		return err
	}
	var extra strings.Builder
	if strings.TrimSpace(result.Response) != "" {
		extra.WriteString("\n\nPartial response before the failure:\n")
		extra.WriteString(result.Response)
	}
	if result.ContextID != "" {
		fmt.Fprintf(&extra, "\n\ncontextId (pass to ask_assistant to resume this conversation): %s", result.ContextID)
	}
	if extra.Len() == 0 {
		return err
	}
	return fmt.Errorf("%w%s", err, extra.String())
}

// AskAssistant is the ask_assistant tool. The assistant may mutate stack state
// (it can call write tools of its own), so this is treated as a write tool and
// is registered only when write tools are enabled.
var AskAssistant = mcpgrafana.MustTool(
	"ask_assistant",
	askAssistantDescription,
	askAssistant,
	mcp.WithTitleAnnotation("Ask Grafana Assistant"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// AddAssistantTools registers the assistant tools with the MCP server. The
// assistant may perform write operations, so it is gated behind enableWriteTools.
func AddAssistantTools(mcp *server.MCPServer, enableWriteTools bool) {
	if enableWriteTools {
		AskAssistant.Register(mcp)
	}
}

// assistantClient issues authenticated requests to the assistant A2A endpoint.
type assistantClient struct {
	httpClient *http.Client
	url        string
}

// newAssistantClient builds a client using the per-request Grafana config from
// the context. BuildTransport applies the correct auth (service-account token,
// on-behalf-of, extra headers, etc.) for the target instance.
func newAssistantClient(ctx context.Context) (*assistantClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("building transport: %w", err)
	}

	return &assistantClient{
		// Intentionally no http.Client.Timeout: this is a long-lived SSE
		// stream and a client-level timeout would abort it mid-read. The whole
		// call is bounded by the caller's context deadline
		// (assistantDefaultTimeout) instead.
		httpClient: &http.Client{Transport: transport},
		url:        strings.TrimSuffix(cfg.URL, "/"),
	}, nil
}

// chat opens the SSE stream (with transient-error retries) and drains it into a
// single aggregated result.
func (c *assistantClient) chat(ctx context.Context, endpoint string, body []byte) (*AskAssistantResult, error) {
	var lastErr error
	for attempt := 1; attempt <= assistantMaxAttempts; attempt++ {
		resp, err := c.openStream(ctx, endpoint, body)
		if err != nil {
			lastErr = err
			if !assistantIsRetryable(err) {
				return nil, err
			}
			if attempt == assistantMaxAttempts {
				break
			}
			if werr := assistantBackoff(ctx, attempt); werr != nil {
				return nil, werr
			}
			continue
		}

		result, drainErr := drainAssistantStream(ctx, resp)
		// Task-level failures are terminal (the agent explicitly failed); do not
		// retry them. Partial output is returned alongside the error.
		return result, drainErr
	}
	return nil, fmt.Errorf("all %d attempts to reach Grafana Assistant failed: %w", assistantMaxAttempts, lastErr)
}

// openStream performs the HTTP POST and returns the response body for a
// successful (200) SSE response. Non-200 responses are converted to errors.
func (c *assistantClient) openStream(ctx context.Context, endpoint string, body []byte) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-App-Source", assistantAppSource)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errAssistantTimeout
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var timeoutErr interface{ Timeout() bool }
		if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			return nil, errAssistantTimeout
		}
		return nil, fmt.Errorf("request to Grafana Assistant failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, &assistantHTTPError{code: resp.StatusCode, body: strings.TrimSpace(string(msg))}
	}

	return resp.Body, nil
}

// assistantHTTPError is a non-200 response from the assistant endpoint.
type assistantHTTPError struct {
	code int
	body string
}

func (e *assistantHTTPError) Error() string {
	if e.body != "" {
		return fmt.Sprintf("Grafana Assistant returned status %d: %s", e.code, e.body)
	}
	return fmt.Sprintf("Grafana Assistant returned status %d", e.code)
}

// assistantIsRetryable reports whether an open-stream error is transient and
// worth retrying.
func assistantIsRetryable(err error) bool {
	if errors.Is(err, errAssistantTimeout) {
		return true
	}
	var httpErr *assistantHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.code {
		case http.StatusTooManyRequests,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	return false
}

// assistantBackoff sleeps with exponential backoff between retry attempts,
// returning early if the context is cancelled.
func assistantBackoff(ctx context.Context, attempt int) error {
	backoff := assistantInitialBackoff << (attempt - 1)
	if backoff > assistantMaxBackoff {
		backoff = assistantMaxBackoff
	}
	select {
	case <-time.After(backoff):
		return nil
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return errAssistantTimeout
		}
		return ctx.Err()
	}
}

// formatAssistantTimeContext renders the time-context XML appended to the prompt
// so the assistant can reason about "now" in the user's timezone.
func formatAssistantTimeContext(t time.Time) string {
	return fmt.Sprintf(
		"<context><time_iso_utc>%s</time_iso_utc><time_iso_local>%s</time_iso_local><timezone>%s</timezone></context>",
		t.UTC().Format(time.RFC3339),
		t.Format(time.RFC3339),
		t.Location().String(),
	)
}

// --- A2A JSON-RPC / SSE protocol types (documented plugin contract) ---

type assistantJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type assistantJSONRPCResponse struct {
	Result json.RawMessage        `json:"result,omitempty"`
	Error  *assistantJSONRPCError `json:"error,omitempty"`
}

type assistantJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type assistantMessagePart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type assistantMessage struct {
	Kind      string                 `json:"kind"`
	Role      string                 `json:"role"`
	Parts     []assistantMessagePart `json:"parts"`
	MessageID string                 `json:"messageId"`
}

type assistantMessageSendParams struct {
	Message   assistantMessage `json:"message"`
	ContextID string           `json:"contextId,omitempty"`
}

type assistantTaskStatus struct {
	State string `json:"state"`
}

type assistantStatusUpdate struct {
	TaskID    string              `json:"taskId"`
	ContextID string              `json:"contextId"`
	Status    assistantTaskStatus `json:"status"`
}

type assistantArtifact struct {
	Name  string                 `json:"name"`
	Parts []assistantMessagePart `json:"parts"`
}

type assistantArtifactUpdate struct {
	TaskID    string            `json:"taskId"`
	ContextID string            `json:"contextId"`
	Artifact  assistantArtifact `json:"artifact"`
}

type assistantTask struct {
	ID        string              `json:"id"`
	ContextID string              `json:"contextId"`
	Status    assistantTaskStatus `json:"status"`
	Artifacts []assistantArtifact `json:"artifacts,omitempty"`
}

// buildAssistantRequest marshals a JSON-RPC message/stream request body.
func buildAssistantRequest(prompt, contextID string) ([]byte, error) {
	params := assistantMessageSendParams{
		Message: assistantMessage{
			Kind:      "message",
			Role:      "user",
			Parts:     []assistantMessagePart{{Kind: "text", Text: prompt}},
			MessageID: uuid.NewString(),
		},
		ContextID: contextID,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(assistantJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      uuid.NewString(),
		Method:  "message/stream",
		Params:  paramsJSON,
	})
}

// assistant task states.
const (
	assistantStateCompleted = "completed"
	assistantStateFailed    = "failed"
	assistantStateCanceled  = "canceled"
	assistantStateTimeout   = "timeout"
)

func assistantStateIsTerminal(state string) bool {
	switch state {
	case assistantStateCompleted, assistantStateFailed, assistantStateCanceled, assistantStateTimeout:
		return true
	}
	return false
}

// terminalStateError maps a terminal task state to its sentinel error (nil for
// a successful completion).
func terminalStateError(state string) error {
	switch state {
	case assistantStateFailed:
		return errAssistantTaskFailed
	case assistantStateCanceled:
		return errAssistantTaskCanceled
	case assistantStateTimeout:
		return errAssistantTaskTimeout
	default:
		return nil
	}
}

// drainAssistantStream consumes the SSE response, accumulating text parts and
// capturing the conversation/context identifiers, until a terminal task state
// is seen or the stream ends. It returns the aggregated result together with any
// terminal/transport error. Partial output is preserved on error.
func drainAssistantStream(ctx context.Context, body io.ReadCloser) (*AskAssistantResult, error) {
	defer func() { _ = body.Close() }()

	// Bound total bytes consumed so a runaway or adversarial stream cannot
	// grow memory without limit over the (up to 5-minute) call. If the cap is
	// hit mid-stream the reply is truncated and falls through to the
	// errAssistantIncompleteStream path below.
	scanner := bufio.NewScanner(io.LimitReader(body, defaultResponseLimitBytes))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var texts []string
	result := &AskAssistantResult{}
	var streamErr error
	var sawTerminal bool

	for scanner.Scan() {
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return finalizeAssistantResult(result, texts, errAssistantTimeout)
			}
			return finalizeAssistantResult(result, texts, ctx.Err())
		}

		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		done, err := parseAssistantEvent(data, result, &texts)
		if err != nil {
			streamErr = err
			break
		}
		if done {
			sawTerminal = true
			break
		}
	}

	if streamErr == nil {
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			streamErr = errAssistantTimeout
		case ctx.Err() != nil:
			streamErr = ctx.Err()
		default:
			if err := scanner.Err(); err != nil {
				streamErr = fmt.Errorf("reading assistant stream: %w", err)
			} else if !sawTerminal {
				// The stream closed cleanly but never reported a terminal task
				// state, so the reply is truncated. Report it rather than
				// passing off possibly-empty text as a completed answer.
				streamErr = errAssistantIncompleteStream
			}
		}
	}

	return finalizeAssistantResult(result, texts, streamErr)
}

// finalizeAssistantResult joins accumulated text and, when no transport error
// occurred, maps a non-successful terminal status to its sentinel error.
func finalizeAssistantResult(result *AskAssistantResult, texts []string, err error) (*AskAssistantResult, error) {
	result.Response = strings.Join(texts, "\n")
	return result, err
}

// parseAssistantEvent parses a single SSE data payload and updates the result.
// It returns done=true when a terminal task state is reached, and a non-nil
// error for JSON-RPC errors or terminal failure states.
func parseAssistantEvent(data string, result *AskAssistantResult, texts *[]string) (bool, error) {
	var resp assistantJSONRPCResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		// Ignore frames we cannot parse (e.g. comments/keep-alives).
		return false, nil
	}
	if resp.Error != nil {
		return false, fmt.Errorf("Grafana Assistant error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return false, nil
	}

	var kindCheck struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(resp.Result, &kindCheck); err != nil {
		return false, nil
	}

	switch kindCheck.Kind {
	case "status-update":
		var update assistantStatusUpdate
		if err := json.Unmarshal(resp.Result, &update); err != nil {
			return false, nil
		}
		applyAssistantIDs(result, update.TaskID, update.ContextID)
		if assistantStateIsTerminal(update.Status.State) {
			return true, terminalStateError(update.Status.State)
		}
		return false, nil

	case "artifact-update":
		var update assistantArtifactUpdate
		if err := json.Unmarshal(resp.Result, &update); err != nil {
			return false, nil
		}
		applyAssistantIDs(result, update.TaskID, update.ContextID)
		if update.Artifact.Name == "step.message" {
			for _, part := range update.Artifact.Parts {
				if part.Kind == "text" && part.Text != "" {
					*texts = append(*texts, part.Text)
				}
			}
		}
		return false, nil

	case "task":
		var task assistantTask
		if err := json.Unmarshal(resp.Result, &task); err != nil {
			return false, nil
		}
		applyAssistantIDs(result, task.ID, task.ContextID)
		// A terminal "task" event may carry the final artifacts in one shot
		// (non-incremental servers). Only use them if we have not already
		// accumulated streamed text.
		if len(*texts) == 0 {
			for _, artifact := range task.Artifacts {
				if artifact.Name != "step.message" {
					continue
				}
				for _, part := range artifact.Parts {
					if part.Kind == "text" && part.Text != "" {
						*texts = append(*texts, part.Text)
					}
				}
			}
		}
		if assistantStateIsTerminal(task.Status.State) {
			return true, terminalStateError(task.Status.State)
		}
		return false, nil
	}

	return false, nil
}

// applyAssistantIDs records the first-seen task and context identifiers.
func applyAssistantIDs(result *AskAssistantResult, taskID, contextID string) {
	if result.ContextID == "" && contextID != "" {
		result.ContextID = contextID
	}
	_ = taskID // taskID is captured for future resubscribe support; not surfaced today.
}
