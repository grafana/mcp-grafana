package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
// a 2xx response.
func notebooksRequest(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
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
		return nil, notebooksAPIError(method, resp.StatusCode, respBody)
	}
	return respBody, nil
}

// notebooksAPIError turns a non-2xx response into an error, adding the two
// things most likely to be wrong when calling this API.
func notebooksAPIError(method string, statusCode int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(statusCode)
	}

	switch statusCode {
	case http.StatusForbidden:
		if strings.Contains(detail, "notebooks feature is not enabled") {
			return fmt.Errorf("%s notebooks: HTTP %d - %s (enable the dashboard.notebooks feature toggle in Grafana)", method, statusCode, detail)
		}
		return fmt.Errorf("%s notebooks: HTTP %d - %s (the caller needs the notebooks:read, notebooks:create, notebooks:write and notebooks:delete actions, or the notebooks:create/write/delete one this tool needs)", method, statusCode, detail)
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

type notebookList struct {
	Items []notebookResource `json:"items"`
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
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	Layout struct {
		Spec struct {
			Cells []json.RawMessage `json:"cells"`
		} `json:"spec"`
	} `json:"layout"`
}

func summarizeNotebook(cfg mcpgrafana.GrafanaConfig, nb notebookResource) notebookSummary {
	summary := notebookSummary{
		UID:       nb.Metadata.Name,
		CreatedAt: nb.Metadata.CreationTimestamp,
		URL:       strings.TrimRight(cfg.URL, "/") + "/notebooks/" + url.PathEscape(nb.Metadata.Name),
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

type ListNotebooksParams struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum number of notebooks to return. Omit to use the Grafana default."`
}

func listNotebooks(ctx context.Context, args ListNotebooksParams) ([]notebookSummary, error) {
	query := url.Values{}
	if args.Limit > 0 {
		query.Set("limit", strconv.Itoa(args.Limit))
	}

	body, err := notebooksRequest(ctx, http.MethodGet, "", query, nil)
	if err != nil {
		return nil, err
	}

	var list notebookList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode notebook list: %w", err)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	summaries := make([]notebookSummary, 0, len(list.Items))
	for _, nb := range list.Items {
		summaries = append(summaries, summarizeNotebook(cfg, nb))
	}
	return summaries, nil
}

var ListNotebooks = mcpgrafana.MustTool(
	"list_notebooks",
	"List Grafana notebooks with their UID, title, tags, cell count and URL. Returns compact summaries without the full spec. "+
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
	searchQuery := map[string]any{
		"apiVersion": "search.grafana.app/v0alpha1",
		"kind":       "SearchQuery",
		"where":      map[string]any{"text": map[string]any{"value": args.Query}},
	}
	if args.Limit > 0 {
		searchQuery["limit"] = args.Limit
	}

	body, err := notebooksRequest(ctx, http.MethodPost, "search", nil, searchQuery)
	if err != nil {
		return nil, err
	}

	var results notebookSearchResults
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("decode notebook search results: %w", err)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	summaries := make([]notebookSummary, 0, len(results.Items))
	for _, item := range results.Items {
		summaries = append(summaries, notebookSummary{
			UID:   item.Resource.Name,
			Title: item.Fields.Title,
			Tags:  item.Fields.Tags,
			URL:   strings.TrimRight(cfg.URL, "/") + "/notebooks/" + url.PathEscape(item.Resource.Name),
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

	body, err := notebooksRequest(ctx, http.MethodGet, args.UID, nil, nil)
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
			return nil, fmt.Errorf("markdown cell %q has no text", c.Kind)
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
	})
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

	// The API replaces the whole resource, so read the current one and send its
	// metadata back untouched: that keeps labels and annotations (a notebook's
	// tags aside, this is where Grafana records who created it) and its resource
	// version, which lets Grafana reject a write made against a stale read.
	current, err := notebooksRequest(ctx, http.MethodGet, args.UID, nil, nil)
	if err != nil {
		return nil, err
	}

	var existing struct {
		Metadata notebookMetadata `json:"metadata"`
		Raw      json.RawMessage  `json:"-"`
	}
	var raw struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(current, &raw); err != nil {
		return nil, fmt.Errorf("decode notebook %s: %w", args.UID, err)
	}
	if err := json.Unmarshal(current, &existing); err != nil {
		return nil, fmt.Errorf("decode notebook %s: %w", args.UID, err)
	}
	if args.ExpectedResourceVersion != "" && args.ExpectedResourceVersion != existing.Metadata.ResourceVersion {
		return nil, fmt.Errorf("notebook %s changed since it was read: at resourceVersion %s, expected %s",
			args.UID, existing.Metadata.ResourceVersion, args.ExpectedResourceVersion)
	}

	body, err := notebooksRequest(ctx, http.MethodPut, args.UID, nil, map[string]any{
		"apiVersion": notebooksAPIVersion,
		"kind":       notebooksKind,
		"metadata":   raw.Metadata,
		"spec":       args.Spec,
	})
	if err != nil {
		return nil, err
	}
	return body, nil
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
	mcpgrafana.RequiresPermissions("notebooks:write", "dashboard.grafana.app/notebooks:update"),
)

type DeleteNotebookParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the notebook to delete."`
}

func deleteNotebook(ctx context.Context, args DeleteNotebookParams) (string, error) {
	if args.UID == "" {
		return "", fmt.Errorf("uid is required")
	}

	if _, err := notebooksRequest(ctx, http.MethodDelete, args.UID, nil, nil); err != nil {
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

func AddNotebookTools(s *mcp.Server, enableWriteTools bool) {
	ListNotebooks.Register(s)
	SearchNotebooks.Register(s)
	GetNotebookByUID.Register(s)
	if enableWriteTools {
		CreateNotebook.Register(s)
		UpdateNotebook.Register(s)
		DeleteNotebook.Register(s)
	}
}
