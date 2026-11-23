package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// Notebooks are a resource in the dashboard API group rather than an endpoint of
// the legacy /api surface, so they are called directly here rather than through
// the generated Grafana OpenAPI client.
const (
	notebooksAPIVersion = "dashboard.grafana.app/v2beta1"
	notebooksKind       = "Notebook"

	// notebookLayoutKind is the only layout kind the notebooks API accepts.
	notebookLayoutKind = "NotebookLayout"
)

// notebooksCollectionURL returns the notebooks collection URL for the org the
// request targets. The API is namespaced, and the namespace is the one Grafana
// reports for the org (default, org-N or stacks-<id>), which is why an orgId
// argument selects the right one.
func notebooksCollectionURL(ctx context.Context) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return "", fmt.Errorf("grafana URL is not configured")
	}

	ns, err := mcpgrafana.GrafanaNamespace(ctx)
	if err != nil {
		return "", err
	}
	if ns == "" {
		return "", fmt.Errorf("grafana reports no namespace, so the notebooks API cannot be addressed")
	}

	return fmt.Sprintf("%s/apis/%s/namespaces/%s/notebooks",
		strings.TrimRight(cfg.URL, "/"), notebooksAPIVersion, url.PathEscape(ns)), nil
}

// notebooksRequest performs a request against the notebooks collection, with
// path appended to it (empty for the collection itself), and returns the body of
// a 2xx response. action is the legacy notebooks:* permission this particular
// call needs, named only in a 403's error message.
func notebooksRequest(ctx context.Context, method, path string, query url.Values, body any, action string) ([]byte, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	collection, err := notebooksCollectionURL(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := collection
	if path != "" {
		endpoint += "/" + url.PathEscape(path)
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = mcpgrafana.DefaultGrafanaClientTimeout
	}
	resp, err := (&http.Client{Transport: transport, Timeout: timeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, notebooksAPIError(method, resp.StatusCode, respBody, action)
	}
	return respBody, nil
}

// notebooksAPIError turns a non-2xx response into an error, adding the two
// things most likely to be wrong when calling this API.
func notebooksAPIError(method string, statusCode int, body []byte, action string) error {
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(statusCode)
	}

	switch statusCode {
	case http.StatusForbidden:
		if strings.Contains(detail, "notebooks feature is not enabled") {
			return fmt.Errorf("%s notebooks: HTTP %d - %s (enable the dashboard.notebooks feature toggle in Grafana)", method, statusCode, detail)
		}
		return fmt.Errorf("%s notebooks: HTTP %d - %s (the caller needs the %s action)", method, statusCode, detail, action)
	case http.StatusNotFound:
		return fmt.Errorf("%s notebooks: HTTP %d - %s (check the UID, and that the Grafana version serves the notebooks API)", method, statusCode, detail)
	default:
		return fmt.Errorf("%s notebooks: HTTP %d - %s", method, statusCode, detail)
	}
}

// notebookResource is the parts of a notebook resource the tools read. The spec
// stays raw so that a caller gets the whole document back and can write it
// again.
type notebookResource struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   notebookMetadata `json:"metadata"`
	Spec       json.RawMessage  `json:"spec"`
}

type notebookMetadata struct {
	Name              string            `json:"name"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

// notebookList is the collection response. Metadata.Continue is Kubernetes'
// pagination token: non-empty when more notebooks exist beyond this page.
type notebookList struct {
	Items    []notebookResource `json:"items"`
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
}

// listNotebooksResult reports whether the list was truncated, since
// notebookList.Metadata.Continue is not itself usable for a further request
// (list_notebooks takes no page cursor), so a caller at least needs to know
// more notebooks exist.
type listNotebooksResult struct {
	Notebooks []notebookSummary `json:"notebooks"`
	Truncated bool              `json:"truncated,omitempty"`
}

// notebookSummary is the compact form returned by the list and search tools, so
// a caller can pick a notebook before paying for its full spec.
type notebookSummary struct {
	UID   string   `json:"uid"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
	// CellCount is only known when the spec was fetched, so search results omit it.
	CellCount *int   `json:"cellCount,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	URL       string `json:"url,omitempty"`
}

type notebookSpecFields struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	// TimeSettings is only read by render_notebook_pdf, to report the range a
	// render actually used when the caller didn't override it.
	TimeSettings notebookTimeSettings `json:"timeSettings"`
	Layout       struct {
		Spec struct {
			Cells []json.RawMessage `json:"cells"`
		} `json:"spec"`
	} `json:"layout"`
}

func summarizeNotebook(baseURL string, nb notebookResource) notebookSummary {
	summary := notebookSummary{
		UID:       nb.Metadata.Name,
		CreatedAt: nb.Metadata.CreationTimestamp,
		URL:       notebookPageURL(baseURL, nb.Metadata.Name),
	}

	var spec notebookSpecFields
	if err := json.Unmarshal(nb.Spec, &spec); err == nil {
		cells := len(spec.Layout.Spec.Cells)
		summary.Title = spec.Title
		summary.Tags = spec.Tags
		summary.CellCount = &cells
	}

	return summary
}

// notebookPageURL is the browser address of a notebook's page, or "" when there
// is no base URL to build it from.
func notebookPageURL(baseURL, uid string) string {
	if baseURL == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/notebooks/" + url.PathEscape(uid)
}

// notebookPageBaseURL is the host a human following a notebook link should land
// on. These URLs go back to the caller as text, so they must be reachable from a
// browser: GRAFANA_URL is often an in-cluster or otherwise internal address,
// while /api/frontend/settings' appUrl is what Grafana itself considers public.
// Falls back to the configured URL when the instance publishes no appUrl (the
// same order every other tool that emits user-facing links uses, via
// grafanaBaseURLFromContext).
//
// Returns "" when the call was scoped to an org other than the one a plain link
// would open in for this identity (deeplinkResolvesInRenderOrg): notebook UIDs
// are unique only within an org, so a link with no org in it would land on the
// wrong notebook, or a 404, in the viewer's own org. get_panel_image omits its
// deeplink for the same reason.
func notebookPageBaseURL(ctx context.Context) string {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if !deeplinkResolvesInRenderOrg(ctx, cfg.OrgID) {
		return ""
	}

	baseURL, err := grafanaBaseURLFromContext(ctx)
	if err != nil {
		// A malformed appUrl must not fail the tool; the configured URL is
		// still better than no link at all.
		return strings.TrimRight(cfg.URL, "/")
	}
	return baseURL
}

type ListNotebooksParams struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum number of notebooks to return. Omit to use the Grafana default."`
}

func listNotebooks(ctx context.Context, args ListNotebooksParams) (listNotebooksResult, error) {
	query := url.Values{}
	if args.Limit > 0 {
		query.Set("limit", strconv.Itoa(args.Limit))
	}

	body, err := notebooksRequest(ctx, http.MethodGet, "", query, nil, "notebooks:read")
	if err != nil {
		return listNotebooksResult{}, err
	}

	var list notebookList
	if err := json.Unmarshal(body, &list); err != nil {
		return listNotebooksResult{}, fmt.Errorf("decode notebook list: %w", err)
	}

	baseURL := notebookPageBaseURL(ctx)
	summaries := make([]notebookSummary, 0, len(list.Items))
	for _, nb := range list.Items {
		summaries = append(summaries, summarizeNotebook(baseURL, nb))
	}
	return listNotebooksResult{Notebooks: summaries, Truncated: list.Metadata.Continue != ""}, nil
}

var ListNotebooks = mcpgrafana.MustTool(
	"list_notebooks",
	"List Grafana notebooks with their UID, title, tags, cell count and URL. Returns compact summaries without the full spec. "+
		"truncated is true when more notebooks exist than were returned in this page. "+
		"Requires the dashboard.notebooks feature toggle to be enabled in Grafana.",
	listNotebooks,
	mcpgrafana.WithTitleAnnotation("List notebooks"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("notebooks:read", "dashboard.grafana.app/notebooks:list"),
)

type SearchNotebooksParams struct {
	Query string `json:"query" jsonschema:"required,description=Text to search for in notebook titles."`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of notebooks to return. Omit to use the Grafana default."`
}

// notebookSearchResults is the subset of the search envelope the tool reads.
type notebookSearchResults struct {
	Items []struct {
		Resource struct {
			Name string `json:"name"`
		} `json:"resource"`
		Fields struct {
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
		} `json:"fields"`
	} `json:"items"`
}

func searchNotebooks(ctx context.Context, args SearchNotebooksParams) ([]notebookSummary, error) {
	if strings.TrimSpace(args.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	// The notebooks collection serves the search API at /notebooks/search.
	// fields is a projection, not a hint: Grafana answers with only a default
	// set (title, folder) unless the extra fields are named here, so without it
	// a match comes back with an empty tags list even when the notebook has
	// tags - verified against a live Grafana (2026-11-06).
	searchQuery := map[string]any{
		"apiVersion": "search.grafana.app/v0alpha1",
		"kind":       "SearchQuery",
		"fields":     []string{"title", "tags"},
		"where":      map[string]any{"text": map[string]any{"value": args.Query}},
	}
	if args.Limit > 0 {
		searchQuery["limit"] = args.Limit
	}

	body, err := notebooksRequest(ctx, http.MethodPost, "search", nil, searchQuery, "notebooks:read")
	if err != nil {
		return nil, err
	}

	var results notebookSearchResults
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("decode notebook search results: %w", err)
	}

	baseURL := notebookPageBaseURL(ctx)
	summaries := make([]notebookSummary, 0, len(results.Items))
	for _, item := range results.Items {
		summaries = append(summaries, notebookSummary{
			UID:   item.Resource.Name,
			Title: item.Fields.Title,
			Tags:  item.Fields.Tags,
			URL:   notebookPageURL(baseURL, item.Resource.Name),
		})
	}
	return summaries, nil
}

var SearchNotebooks = mcpgrafana.MustTool(
	"search_notebooks",
	"Search Grafana notebooks by title text. Returns matching notebooks with their UID, title, tags and URL.",
	searchNotebooks,
	mcpgrafana.WithTitleAnnotation("Search notebooks"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("notebooks:read", "dashboard.grafana.app/notebooks:list"),
)

type GetNotebookByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the notebook."`
}

func getNotebookByUID(ctx context.Context, args GetNotebookByUIDParams) (json.RawMessage, error) {
	if args.UID == "" {
		return nil, fmt.Errorf("uid is required")
	}

	body, err := notebooksRequest(ctx, http.MethodGet, args.UID, nil, nil, "notebooks:read")
	if err != nil {
		return nil, err
	}
	return body, nil
}

var GetNotebookByUID = mcpgrafana.MustTool(
	"get_notebook_by_uid",
	"Get a Grafana notebook by its UID. Returns the full notebook resource and its spec, which can be edited and written back.",
	getNotebookByUID,
	mcpgrafana.WithTitleAnnotation("Get notebook by UID"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("notebooks:read", "dashboard.grafana.app/notebooks:get"),
)

type notebookTimeSettings struct {
	Timezone             string   `json:"timezone"`
	From                 string   `json:"from"`
	To                   string   `json:"to"`
	AutoRefresh          string   `json:"autoRefresh"`
	AutoRefreshIntervals []string `json:"autoRefreshIntervals"`
	HideTimepicker       bool     `json:"hideTimepicker"`
	FiscalYearStartMonth int      `json:"fiscalYearStartMonth"`
}

type notebookLayoutItemSpec struct {
	Element struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"element"`
	Source string `json:"source"`
}

type notebookLayoutItem struct {
	Kind string                 `json:"kind"`
	Spec notebookLayoutItemSpec `json:"spec"`
}

// notebookSpec is the subset of the notebook spec create_notebook writes. The
// values mirror the defaults the Grafana UI uses for a new notebook.
type notebookSpec struct {
	Title        string               `json:"title"`
	Tags         []string             `json:"tags"`
	TimeSettings notebookTimeSettings `json:"timeSettings"`
	Elements     map[string]any       `json:"elements"`
	Layout       struct {
		Kind string `json:"kind"`
		Spec struct {
			Cells []notebookLayoutItem `json:"cells"`
		} `json:"spec"`
	} `json:"layout"`
}

// notebookCell is one cell to create, in document order. Kind selects which of
// the content fields is used, which keeps the schema flat: a discriminated union
// of cell kinds would be a more expensive schema for no extra expressiveness.
type notebookCell struct {
	Kind     string `json:"kind" jsonschema:"required,description=Cell kind: markdown or code."`
	Text     string `json:"text,omitempty" jsonschema:"description=Markdown text of a markdown cell."`
	Code     string `json:"code,omitempty" jsonschema:"description=Code of a code cell."`
	Language string `json:"language,omitempty" jsonschema:"description=Language of a code cell such as promql or sql. Defaults to promql."`
}

func (c notebookCell) content() (map[string]any, error) {
	switch strings.ToLower(c.Kind) {
	case "markdown":
		if strings.TrimSpace(c.Text) == "" {
			return nil, fmt.Errorf("markdown cell has no text")
		}
		return map[string]any{"kind": "Markdown", "spec": map[string]any{"text": c.Text}}, nil
	case "code":
		if strings.TrimSpace(c.Code) == "" {
			return nil, fmt.Errorf("code cell has no code")
		}
		language := c.Language
		if language == "" {
			language = "promql"
		}
		return map[string]any{"kind": "Code", "spec": map[string]any{"language": language, "code": c.Code}}, nil
	default:
		return nil, fmt.Errorf("unsupported cell kind %q: use markdown or code", c.Kind)
	}
}

// newNotebookSpec builds the spec create_notebook writes, which mirrors what the
// Grafana UI writes for a new notebook: every cell is a "Cell" element, named
// after its kind and position, referenced from one layout item authored by the
// user.
func newNotebookSpec(title string, tags []string, cells []notebookCell) (notebookSpec, error) {
	spec := notebookSpec{
		Title: title,
		Tags:  tags,
		TimeSettings: notebookTimeSettings{
			Timezone:             "browser",
			From:                 "now-6h",
			To:                   "now",
			AutoRefresh:          "",
			AutoRefreshIntervals: []string{"5s", "10s", "30s", "1m", "5m", "15m", "30m", "1h", "2h", "1d"},
			HideTimepicker:       false,
			FiscalYearStartMonth: 0,
		},
		Elements: map[string]any{},
	}
	spec.Layout.Kind = notebookLayoutKind
	spec.Layout.Spec.Cells = []notebookLayoutItem{}

	seen := map[string]int{}
	for i, c := range cells {
		content, err := c.content()
		if err != nil {
			return notebookSpec{}, fmt.Errorf("cell %d: %w", i+1, err)
		}

		kind := strings.ToLower(c.Kind)
		seen[kind]++
		name := fmt.Sprintf("%s-%d", kind, seen[kind])
		spec.Elements[name] = map[string]any{"kind": "Cell", "spec": map[string]any{"content": content}}

		item := notebookLayoutItem{Kind: "NotebookLayoutItem"}
		item.Spec.Element.Kind = "ElementReference"
		item.Spec.Element.Name = name
		item.Spec.Source = "user"
		spec.Layout.Spec.Cells = append(spec.Layout.Spec.Cells, item)
	}

	return spec, nil
}

type CreateNotebookParams struct {
	Title string         `json:"title" jsonschema:"required,description=The title of the notebook."`
	Tags  []string       `json:"tags,omitempty" jsonschema:"description=Optional tags to attach to the notebook."`
	Cells []notebookCell `json:"cells,omitempty" jsonschema:"description=Optional cells in document order. The notebook is created with no cells when omitted."`
}

func createNotebook(ctx context.Context, args CreateNotebookParams) (json.RawMessage, error) {
	if strings.TrimSpace(args.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}

	tags := args.Tags
	if tags == nil {
		tags = []string{}
	}

	spec, err := newNotebookSpec(args.Title, tags, args.Cells)
	if err != nil {
		return nil, err
	}

	body, err := notebooksRequest(ctx, http.MethodPost, "", nil, map[string]any{
		"apiVersion": notebooksAPIVersion,
		"kind":       notebooksKind,
		"metadata":   map[string]any{"generateName": "notebook-"},
		"spec":       spec,
	}, "notebooks:create")
	if err != nil {
		return nil, err
	}
	return body, nil
}

var CreateNotebook = mcpgrafana.MustTool(
	"create_notebook",
	"Create a Grafana notebook with a title and optional tags. Pass cells to start it with Markdown and code cells in document order. "+
		"Returns the created notebook resource.",
	createNotebook,
	mcpgrafana.WithTitleAnnotation("Create notebook"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("notebooks:create", "dashboard.grafana.app/notebooks:create"),
)

type UpdateNotebookParams struct {
	UID  string         `json:"uid" jsonschema:"required,description=The UID of the notebook to update."`
	Spec map[string]any `json:"spec" jsonschema:"required,description=The complete notebook spec to write; the layout kind must remain NotebookLayout."`
	// ExpectedResourceVersion, when set, fails the update instead of overwriting
	// a notebook someone else changed after the caller read it.
	ExpectedResourceVersion string `json:"expectedResourceVersion,omitempty" jsonschema:"description=Optional resourceVersion the spec was read from; the update is rejected if the notebook has changed since."`
}

func updateNotebook(ctx context.Context, args UpdateNotebookParams) (json.RawMessage, error) {
	if args.UID == "" {
		return nil, fmt.Errorf("uid is required")
	}
	if args.Spec == nil {
		return nil, fmt.Errorf("spec is required")
	}
	if err := validateNotebookSpec(args.Spec); err != nil {
		return nil, err
	}

	// The API replaces the whole resource, so read the current one and send its
	// metadata back untouched: that keeps labels and annotations (a notebook's
	// tags aside, this is where Grafana records who created it) and its resource
	// version, which lets Grafana reject a write made against a stale read.
	current, err := notebooksRequest(ctx, http.MethodGet, args.UID, nil, nil, "notebooks:read")
	if err != nil {
		return nil, err
	}

	var resource struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(current, &resource); err != nil {
		return nil, fmt.Errorf("decode notebook %s: %w", args.UID, err)
	}
	var metadata notebookMetadata
	if err := json.Unmarshal(resource.Metadata, &metadata); err != nil {
		return nil, fmt.Errorf("decode notebook %s metadata: %w", args.UID, err)
	}
	if args.ExpectedResourceVersion != "" && args.ExpectedResourceVersion != metadata.ResourceVersion {
		return nil, fmt.Errorf("notebook %s changed since it was read: at resourceVersion %s, expected %s",
			args.UID, metadata.ResourceVersion, args.ExpectedResourceVersion)
	}

	body, err := notebooksRequest(ctx, http.MethodPut, args.UID, nil, map[string]any{
		"apiVersion": notebooksAPIVersion,
		"kind":       notebooksKind,
		"metadata":   resource.Metadata,
		"spec":       args.Spec,
	}, "notebooks:write")
	if err != nil {
		return nil, err
	}
	return body, nil
}

// validateNotebookSpec rejects a spec before it reaches the PUT, which would
// otherwise replace the whole resource with whatever was sent. An empty spec or
// one whose layout isn't a NotebookLayout is never a notebook the UI or these
// tools would have produced.
func validateNotebookSpec(spec map[string]any) error {
	if len(spec) == 0 {
		return fmt.Errorf("spec must not be empty")
	}
	layout, ok := spec["layout"].(map[string]any)
	if !ok {
		return fmt.Errorf("spec.layout is required and must be an object")
	}
	if kind, _ := layout["kind"].(string); kind != notebookLayoutKind {
		return fmt.Errorf("spec.layout.kind must be %q, got %q", notebookLayoutKind, layout["kind"])
	}
	return nil
}

var UpdateNotebook = mcpgrafana.MustTool(
	"update_notebook",
	"Update a Grafana notebook by replacing its spec. Send the complete spec, and optionally the resourceVersion it was read at. "+
		"The notebook's labels and annotations are preserved. Returns the updated notebook resource.",
	updateNotebook,
	mcpgrafana.WithTitleAnnotation("Update notebook"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	// The update reads the current notebook to send its metadata back, so it
	// needs the read actions as well as the write one.
	mcpgrafana.RequiresPermissions("notebooks:read", "dashboard.grafana.app/notebooks:get"),
	mcpgrafana.RequiresPermissions("notebooks:write", "dashboard.grafana.app/notebooks:update"),
)

type DeleteNotebookParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the notebook to delete."`
}

func deleteNotebook(ctx context.Context, args DeleteNotebookParams) (string, error) {
	if args.UID == "" {
		return "", fmt.Errorf("uid is required")
	}

	if _, err := notebooksRequest(ctx, http.MethodDelete, args.UID, nil, nil, "notebooks:delete"); err != nil {
		return "", err
	}
	return fmt.Sprintf("Notebook %s deleted successfully", args.UID), nil
}

var DeleteNotebook = mcpgrafana.MustTool(
	"delete_notebook",
	"Delete a Grafana notebook by its UID. This action cannot be undone.",
	deleteNotebook,
	mcpgrafana.WithTitleAnnotation("Delete notebook"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("notebooks:delete", "dashboard.grafana.app/notebooks:delete"),
)

type RenderNotebookPDFParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the notebook to render."`
	// From, To and TimeZone feed the time picker embedded in the exported
	// document, so panels and time-dependent cells show the requested range.
	From     string `json:"from,omitempty" jsonschema:"description=Start of the rendered time range\\, e.g. now-6h or an absolute timestamp. Defaults to what Grafana's notebook page opens with (now-6h)."`
	To       string `json:"to,omitempty" jsonschema:"description=End of the rendered time range\\, e.g. now. Defaults to now."`
	TimeZone string `json:"timeZone,omitempty" jsonschema:"description=IANA time zone the exported document renders times in\\, e.g. Europe/Amsterdam. Defaults to the renderer's zone (UTC)."`
	Theme    string `json:"theme,omitempty" jsonschema:"description=Theme for the rendered document: light or dark. Defaults to dark."`
	Timeout  *int   `json:"timeout,omitempty" jsonschema:"description=Rendering timeout in seconds. Defaults to 60\\, capped at 120."`
}

// notebookRenderURL builds the Scenes export URL for a notebook. Grafana serves
// the notebook page itself under /render/notebooks/<uid> (through the image
// renderer, like /render/d/<uid> for dashboards), and the /render subpath
// exports that page in the format named by ?encoding - pdf or png.
func notebookRenderURL(cfg mcpgrafana.GrafanaConfig, args RenderNotebookPDFParams) string {
	params := url.Values{}
	params.Set("encoding", "pdf")
	// The notebook page is a Scenes app, whose URL parser only recognizes ISO
	// 8601 at exactly 24 characters (with milliseconds) and otherwise falls
	// back to parseInt - so an absolute timestamp like the jsonschema
	// description invites (e.g. "2026-04-28T12:45:00Z", 20 chars) would be
	// silently read as epoch 2026ms and render January 1970. toGrafanaTimeParam
	// is the same conversion get_panel_image uses for the same reason.
	if args.From != "" {
		params.Set("from", toGrafanaTimeParam(args.From, false))
	}
	if args.To != "" {
		params.Set("to", toGrafanaTimeParam(args.To, true))
	}
	if args.TimeZone != "" {
		// The notebook UI sends both: timezone is the time picker's parameter
		// and tz is the renderer's. Setting only one of them leaves part of the
		// page in the renderer's zone.
		params.Set("timezone", args.TimeZone)
		params.Set("tz", args.TimeZone)
	}
	if args.Theme != "" {
		params.Set("theme", args.Theme)
	}

	// Select the org to render from. The image renderer drives a headless browser
	// whose request org comes from the targetOrgId query param; the
	// X-Grafana-Org-Id header the transport adds is ignored on the render path.
	// Scoping only by the header is therefore not enough: the notebook GET below
	// would resolve in the target org while the exported page came from the
	// identity's own org - and UIDs are unique only within an org, so that is a
	// silent 200 carrying another org's document. targetOrgId rather than orgId
	// for the same reason get_panel_image uses it: orgId is read by the frontend,
	// which persists an org switch via /api/user/using, changing the caller's
	// active org for every later request.
	if cfg.OrgID > 0 {
		params.Set("targetOrgId", strconv.FormatInt(cfg.OrgID, 10))
	}

	return fmt.Sprintf("%s/render/notebooks/%s/render?%s",
		strings.TrimRight(cfg.URL, "/"), url.PathEscape(args.UID), params.Encode())
}

// notebookRenderTimeout mirrors get_panel_image: the configured client timeout
// is far too short for a headless browser render, so rendering has its own
// budget and a caller-supplied override.
func notebookRenderTimeout(args RenderNotebookPDFParams) time.Duration {
	if args.Timeout == nil || *args.Timeout <= 0 {
		return defaultRenderTimeout
	}
	return time.Duration(min(*args.Timeout, maxRenderTimeoutSecs)) * time.Second
}

func renderNotebookPDF(ctx context.Context, args RenderNotebookPDFParams) (*mcp.CallToolResult, error) {
	if args.UID == "" {
		return nil, fmt.Errorf("uid is required")
	}
	if args.Theme != "" && args.Theme != "light" && args.Theme != "dark" {
		return nil, fmt.Errorf("theme must be light or dark, got %q", args.Theme)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	// The export renders whatever the renderer can load, and a notebook Grafana
	// cannot find is not a 404 on that route: it renders an error page into the
	// PDF and still answers 200. Resolve the notebook through the API first so a
	// bad UID fails loudly, and to have its title for the result summary.
	current, err := notebooksRequest(ctx, http.MethodGet, args.UID, nil, nil, "notebooks:read")
	if err != nil {
		return nil, err
	}
	var notebook notebookResource
	if err := json.Unmarshal(current, &notebook); err != nil {
		return nil, fmt.Errorf("decode notebook %s: %w", args.UID, err)
	}
	var spec notebookSpecFields
	if err := json.Unmarshal(notebook.Spec, &spec); err != nil {
		return nil, fmt.Errorf("decode notebook %s spec: %w", args.UID, err)
	}

	// The org to render from comes from config.OrgID, which the per-call orgId
	// argument sets when dynamic multi-org is enabled and which otherwise
	// carries the connection's org. It reaches the renderer as the targetOrgId
	// query param (see notebookRenderURL) - the render path ignores the
	// X-Grafana-Org-Id header the transport sends, which is what scopes the API
	// request below.
	transport, err := mcpgrafana.BuildTransport(&cfg, cfg.HTTPTransport())
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	renderURL := notebookRenderURL(cfg, args)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, renderURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/pdf")

	client := &http.Client{Transport: transport, Timeout: notebookRenderTimeout(args)}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("render notebook %s: %w", args.UID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if resp.StatusCode == http.StatusNotFound {
			// The notebook itself was already resolved above, so this 404 is the
			// /render route itself: either no Image Renderer is configured, or this
			// Grafana version doesn't serve notebook rendering at all.
			return nil, fmt.Errorf("image renderer not available: HTTP 404 - %s (ensure the Grafana Image Renderer service is installed and configured, and that this Grafana version serves the notebook render route)", strings.TrimSpace(string(detail)))
		}
		return nil, fmt.Errorf("render notebook %s: HTTP %d - %s", args.UID, resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	// Grafana answers with the login page (200 text/html, after following the
	// redirect to /login) when the caller is not authorized to view the page, so
	// the content type, not the status code, says whether this is a PDF.
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/pdf") {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("render notebook %s: expected a PDF, got %q - %s (the caller needs to be able to view the notebook in Grafana)",
			args.UID, contentType, strings.TrimSpace(string(detail)))
	}

	pdf, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		if errors.Is(err, errResponseBodyTooLarge) {
			err = fmt.Errorf("%w of %d bytes; try a shorter time range or fewer cells", errResponseBodyTooLarge, defaultResponseLimitBytes)
		}
		return nil, fmt.Errorf("read notebook %s PDF: %w", args.UID, err)
	}

	// When from/to aren't passed, none is sent to the renderer (notebookRenderURL
	// leaves them unset), so the page opens with its own saved timeSettings
	// rather than the now-6h/now the notebook UI defaults a *new* notebook to.
	// Report the range that was actually sent, falling back to the notebook's
	// saved range rather than that unrelated default.
	timeRange := fmt.Sprintf("%s to %s",
		orDefault(args.From, orDefault(spec.TimeSettings.From, "now-6h")),
		orDefault(args.To, orDefault(spec.TimeSettings.To, "now")))
	if args.TimeZone != "" {
		timeRange += " (" + args.TimeZone + ")"
	}
	summary := fmt.Sprintf("Rendered notebook %q (%s) as a PDF: %d bytes, time range %s.",
		spec.Title, args.UID, len(pdf), timeRange)
	if pageURL := notebookPageURL(notebookPageBaseURL(ctx), args.UID); pageURL != "" {
		summary += " Notebook page: " + pageURL
	}

	// The PDF travels as an embedded resource so a client can write the bytes
	// out as-is; the text summary keeps the call useful for clients that do not
	// surface resource blobs.
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: summary},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI:      fmt.Sprintf("grafana://notebooks/%s.pdf", url.PathEscape(args.UID)),
				MIMEType: "application/pdf",
				Blob:     pdf,
			}},
		},
	}, nil
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

var RenderNotebookPDF = mcpgrafana.MustTool(
	"render_notebook_pdf",
	"Export a Grafana notebook as a PDF, the way the notebook's UI export does: the page is rendered by Grafana's image renderer and returned as an embedded application/pdf resource. "+
		"The document carries the notebook title, its cells rendered in order, the time picker value and page numbers. "+
		"The time range and time zone arguments set what the exported document shows. Requires the dashboard.notebooks feature toggle and a configured Grafana Image Renderer.",
	renderNotebookPDF,
	mcpgrafana.WithTitleAnnotation("Export notebook as PDF"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	// Rendering reads the notebook resource before exporting the page.
	mcpgrafana.RequiresPermissions("notebooks:read", "dashboard.grafana.app/notebooks:get"),
)
