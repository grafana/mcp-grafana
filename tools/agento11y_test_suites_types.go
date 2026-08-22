package tools

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Wire types for the test suites of the Agent Observability eval control plane
// (/eval/test-suites on the grafana-agento11y-app plugin resources proxy).
//
// A suite is versioned. Test cases belong to one version, never to the suite,
// so every test case route carries both a suite ID and a version.
//
// A version is a draft until it is published; a published version is frozen and
// rejects every test case edit.

// Agento11yTestSuite is one test suite. versions is filled in only by the
// single-suite route; a list row carries the suite without them.
type Agento11yTestSuite struct {
	TenantID    string   `json:"tenant_id,omitempty"`
	SuiteID     string   `json:"suite_id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// LatestVersion names the most recently published version, and stays empty
	// until a version is published for the first time.
	LatestVersion string                      `json:"latest_version,omitempty"`
	Versions      []Agento11yTestSuiteVersion `json:"versions,omitempty"`
	CreatedBy     string                      `json:"created_by,omitempty"`
	UpdatedBy     string                      `json:"updated_by,omitempty"`
	CreatedAt     time.Time                   `json:"created_at,omitzero"`
	UpdatedAt     time.Time                   `json:"updated_at,omitzero"`
}

// Agento11yTestSuiteVersion is one version of a suite. A suite has at most one
// draft at a time, which is the only version that accepts test case edits.
type Agento11yTestSuiteVersion struct {
	TenantID string `json:"tenant_id,omitempty"`
	SuiteID  string `json:"suite_id"`
	Version  string `json:"version"`
	// TestCaseCount is counted per request rather than stored.
	TestCaseCount int    `json:"test_case_count"`
	Changelog     string `json:"changelog,omitempty"`
	Published     bool   `json:"published"`
	// SourceVersion names the published version this one was cloned from, and is
	// empty on the first version of a suite.
	SourceVersion string     `json:"source_version,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at,omitzero"`
	PublishedBy   string     `json:"published_by,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}

// Agento11yTestCase is one case of one suite version. input and expected are
// free-form: the runner decides what a case feeds the agent and what counts as
// the expected answer.
type Agento11yTestCase struct {
	TenantID     string                 `json:"tenant_id,omitempty"`
	SuiteID      string                 `json:"suite_id,omitempty"`
	SuiteVersion string                 `json:"suite_version,omitempty"`
	TestCaseID   string                 `json:"test_case_id"`
	Name         string                 `json:"name,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	Category     string                 `json:"category,omitempty"`
	Input        map[string]any         `json:"input"`
	Expected     map[string]any         `json:"expected,omitempty"`
	Metadata     map[string]any         `json:"metadata,omitempty"`
	ArtifactRefs []Agento11yArtifactRef `json:"artifact_refs,omitempty"`
	CreatedAt    time.Time              `json:"created_at,omitzero"`
	UpdatedAt    time.Time              `json:"updated_at,omitzero"`
}

// agento11yTestSuiteFields are the pagination parameters shared by the read and
// write variants of agento11y_evals_read/agento11y_evals_write. The ID and body
// parameters are declared on each variant separately, because their guidance
// names the operations that use them and the read variant must not advertise
// operations it rejects.
type agento11yTestSuiteFields struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for the list operations)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor\\, echoed back exactly and never constructed or incremented"`
}

// agento11yTestSuiteReadRequest is the input of a test suite read operation,
// assembled by either tool variant.
type agento11yTestSuiteReadRequest struct {
	agento11yTestSuiteFields

	SuiteID    string
	Version    string
	TestCaseID string
}

// validateOperation validates the read operations of
// agento11y_evals_read/agento11y_evals_write. Operations it does not handle return
// errAgento11yUnknownOperation.
//
// There is no operation for reading one version on its own: the route exists on
// the plugin proxy but answers 400 upstream. get_suite embeds every version.
func (r agento11yTestSuiteReadRequest) validateOperation(operation string) error {
	switch operation {
	case "list_suites":
		return nil
	case "get_suite":
		return agento11yRequireSuiteID(operation, r.SuiteID)
	case "list_test_cases":
		return agento11yRequireVersion(operation, r.SuiteID, r.Version)
	case "get_test_case":
		if err := agento11yRequireVersion(operation, r.SuiteID, r.Version); err != nil {
			return err
		}
		if r.TestCaseID == "" {
			return fmt.Errorf("test_case_id is required for %q operation", operation)
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

func agento11yRequireSuiteID(operation, suiteID string) error {
	if suiteID == "" {
		return fmt.Errorf("suite_id is required for %q operation", operation)
	}
	return nil
}

// agento11yRequireVersion checks the pair every versioned route needs. A test
// case belongs to one version of one suite, so a suite ID alone does not
// address it.
func agento11yRequireVersion(operation, suiteID, version string) error {
	if err := agento11yRequireSuiteID(operation, suiteID); err != nil {
		return err
	}
	if version == "" {
		return fmt.Errorf("version is required for %q operation (read the versions of the suite from 'get_suite')", operation)
	}
	return nil
}

const agento11yTestSuiteReadOperations = "list_suites, get_suite, list_test_cases, get_test_case"

// agento11yTestSuiteWriteRequest is the input of a test suite write
// operation, assembled by agento11y_evals_write from its flat params.
type agento11yTestSuiteWriteRequest struct {
	SuiteID    string
	Version    string
	TestCaseID string

	Name        *string
	Description *string
	Tags        *[]string

	Changelog  string
	EmptyDraft bool

	Category     string
	Input        map[string]any
	Expected     map[string]any
	Metadata     map[string]any
	ArtifactRefs []Agento11yArtifactRef
}

// agento11yParamUse pairs one declared parameter with the operations whose
// route reads it.
type agento11yParamUse struct {
	name       string
	set        bool
	operations []string
}

func agento11yFirstUnusedParam(operation string, params []agento11yParamUse) (string, []string) {
	for _, param := range params {
		if param.set && !slices.Contains(param.operations, operation) {
			return param.name, param.operations
		}
	}
	return "", nil
}

// agento11yUnusedParamError names the operations that do read the parameter, so a
// caller that set it on the wrong one is told where it belongs.
func agento11yUnusedParamError(param, operation string, readBy []string) error {
	quoted := make([]string, len(readBy))
	for i, name := range readBy {
		quoted[i] = "'" + name + "'"
	}
	return fmt.Errorf("%s is not accepted by %q operation, which would drop it: it is only read by %s", param, operation, strings.Join(quoted, ", "))
}
