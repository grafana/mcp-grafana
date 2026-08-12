package tools

import (
	"errors"
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
// read-write variants of agento11y_manage_test_suites. The ID and body
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
// agento11y_manage_test_suites. Operations it does not handle return
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

const agento11yTestSuiteAllOperations = agento11yTestSuiteReadOperations +
	", create_suite, update_suite, create_draft_version, publish_version, upsert_test_case, delete_test_case"

// ManageAgento11yTestSuitesReadParams is the param struct for the read-only
// version of agento11y_manage_test_suites.
type ManageAgento11yTestSuitesReadParams struct {
	agento11yTestSuiteFields

	Operation  string `json:"operation" jsonschema:"required,enum=list_suites,enum=get_suite,enum=list_test_cases,enum=get_test_case,description=The operation to perform: 'list_suites' for the test suites in this tenant\\, 'get_suite' for one suite with its full version history\\, 'list_test_cases' for the cases of one suite version page by page\\, 'get_test_case' for one case in full"`
	SuiteID    string `json:"suite_id,omitempty" jsonschema:"description=Test suite ID from 'list_suites' (required for 'get_suite'\\, 'list_test_cases'\\, and 'get_test_case')"`
	Version    string `json:"version,omitempty" jsonschema:"description=Suite version such as 'v3'\\, taken from the versions list of 'get_suite' (required for 'list_test_cases' and 'get_test_case'). There is no operation for reading a version on its own."`
	TestCaseID string `json:"test_case_id,omitempty" jsonschema:"description=Test case ID from 'list_test_cases' (required for 'get_test_case')"`
}

func (p ManageAgento11yTestSuitesReadParams) readRequest() agento11yTestSuiteReadRequest {
	return agento11yTestSuiteReadRequest{
		agento11yTestSuiteFields: p.agento11yTestSuiteFields,
		SuiteID:                  p.SuiteID,
		Version:                  p.Version,
		TestCaseID:               p.TestCaseID,
	}
}

func (p ManageAgento11yTestSuitesReadParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yTestSuiteReadOperations)
	}
	return err
}

// ManageAgento11yTestSuitesReadWriteParams is the param struct for the
// read-write version of agento11y_manage_test_suites.
type ManageAgento11yTestSuitesReadWriteParams struct {
	agento11yTestSuiteFields

	Operation  string `json:"operation" jsonschema:"required,enum=list_suites,enum=get_suite,enum=list_test_cases,enum=get_test_case,enum=create_suite,enum=update_suite,enum=create_draft_version,enum=publish_version,enum=upsert_test_case,enum=delete_test_case,description=The operation to perform. Reads: 'list_suites'\\, 'get_suite'\\, 'list_test_cases'\\, 'get_test_case'. Writes: 'create_suite'\\, 'update_suite' (PATCH the name\\, description\\, or tags)\\, 'create_draft_version' (open an editable version)\\, 'publish_version' (freeze it)\\, 'upsert_test_case' (write a whole case into a draft)\\, 'delete_test_case'"`
	SuiteID    string `json:"suite_id,omitempty" jsonschema:"description=Test suite ID from 'list_suites'. Required for every operation except 'list_suites'\\, which takes none\\, and 'create_suite'\\, where it is an optional caller-chosen ID that must not already exist."`
	Version    string `json:"version,omitempty" jsonschema:"description=Suite version such as 'v3'\\, taken from the versions list of 'get_suite'. Required for 'publish_version'\\, 'list_test_cases'\\, 'get_test_case'\\, 'upsert_test_case'\\, and 'delete_test_case'. 'create_draft_version' assigns the next version itself\\, so do not send one."`
	TestCaseID string `json:"test_case_id,omitempty" jsonschema:"description=Test case ID. Required for 'get_test_case' and 'delete_test_case'. For 'upsert_test_case' it selects an existing case to replace; omit it to have one created."`

	Name        *string   `json:"name,omitempty" jsonschema:"description=Suite name. Required and non-blank for 'create_suite'. For 'update_suite' an omitted name is left unchanged and a blank one is rejected. For 'upsert_test_case' it names the case."`
	Description *string   `json:"description,omitempty" jsonschema:"description=Suite or test case description. An omitted field is left unchanged by 'update_suite' and an explicitly empty string clears it."`
	Tags        *[]string `json:"tags,omitempty" jsonschema:"description=Replacement tag list for 'update_suite' and 'upsert_test_case'. It overwrites the whole list rather than adding to it\\, so read the current tags first; an explicitly empty array clears them."`

	Changelog  string `json:"changelog,omitempty" jsonschema:"description=Note describing what the new version changes (for 'create_draft_version')"`
	EmptyDraft bool   `json:"empty_draft,omitempty" jsonschema:"description=Start the new version with no test cases (for 'create_draft_version'). The default copies every case of the latest published version\\, which is what an edit to an existing suite wants."`

	Category     string                 `json:"category,omitempty" jsonschema:"description=Test case category (for 'upsert_test_case')"`
	Input        map[string]any         `json:"input,omitempty" jsonschema:"description=What the case feeds the agent (required and non-empty for 'upsert_test_case'). The shape is free-form and decided by the runner\\, so copy an existing case of the suite before inventing one."`
	Expected     map[string]any         `json:"expected,omitempty" jsonschema:"description=What the case expects back (for 'upsert_test_case'). Free-form\\, and read by the evaluators rather than compared by the API."`
	Metadata     map[string]any         `json:"metadata,omitempty" jsonschema:"description=Free-form metadata object (for 'upsert_test_case')"`
	ArtifactRefs []Agento11yArtifactRef `json:"artifact_refs,omitempty" jsonschema:"description=Artifacts the case refers to\\, each an artifact_id with its name and kind (for 'upsert_test_case')"`
}

func (p ManageAgento11yTestSuitesReadWriteParams) readRequest() agento11yTestSuiteReadRequest {
	return agento11yTestSuiteReadRequest{
		agento11yTestSuiteFields: p.agento11yTestSuiteFields,
		SuiteID:                  p.SuiteID,
		Version:                  p.Version,
		TestCaseID:               p.TestCaseID,
	}
}

func (p ManageAgento11yTestSuitesReadWriteParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if !errors.Is(err, errAgento11yUnknownOperation) {
		if err != nil {
			return err
		}
		return p.rejectWriteFields()
	}

	if err := p.validateWriteOperation(); err != nil {
		return err
	}
	// Each write route sends its own body, so a field belonging to a sibling
	// operation is refused rather than dropped on the way out.
	if field, readBy := p.unusedBodyField(); field != "" {
		return agento11yUnusedParamError(field, p.Operation, readBy)
	}
	if field, readBy := p.unusedWriteIDField(); field != "" {
		return agento11yUnusedParamError(field, p.Operation, readBy)
	}
	return nil
}

func (p ManageAgento11yTestSuitesReadWriteParams) validateWriteOperation() error {
	switch p.Operation {
	case "create_suite":
		if p.Name == nil || strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name is required for 'create_suite' operation")
		}
		return nil
	case "update_suite":
		if err := agento11yRequireSuiteID(p.Operation, p.SuiteID); err != nil {
			return err
		}
		if p.Name == nil && p.Description == nil && p.Tags == nil {
			return fmt.Errorf("at least one of name, description, or tags is required for 'update_suite' operation (an omitted field is left unchanged)")
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name must not be blank for 'update_suite' operation (omit it to leave the name unchanged)")
		}
		return nil
	case "create_draft_version":
		if err := agento11yRequireSuiteID(p.Operation, p.SuiteID); err != nil {
			return err
		}
		if p.Version != "" {
			return fmt.Errorf("version must not be set for 'create_draft_version' operation: the API assigns the next version itself")
		}
		return nil
	case "publish_version":
		return agento11yRequireVersion(p.Operation, p.SuiteID, p.Version)
	case "upsert_test_case":
		if err := agento11yRequireVersion(p.Operation, p.SuiteID, p.Version); err != nil {
			return err
		}
		if len(p.Input) == 0 {
			return fmt.Errorf("input is required and must not be empty for 'upsert_test_case' operation")
		}
		return nil
	case "delete_test_case":
		if err := agento11yRequireVersion(p.Operation, p.SuiteID, p.Version); err != nil {
			return err
		}
		if p.TestCaseID == "" {
			return fmt.Errorf("test_case_id is required for 'delete_test_case' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yTestSuiteAllOperations)
	}
}

// rejectWriteFields refuses a write body field on a read operation. This variant
// declares the fields for the write operations, so a read operation would
// otherwise accept and drop them: 'list_suites' with tags reads like a filter
// and would answer with every suite in the tenant.
func (p ManageAgento11yTestSuitesReadWriteParams) rejectWriteFields() error {
	if field, _ := p.unusedBodyField(); field != "" {
		return fmt.Errorf("%s is only read by the write operations, not by %q: the read operations take no filter beyond suite_id, version, and test_case_id", field, p.Operation)
	}
	return nil
}

// agento11yParamUse pairs one declared parameter with the operations whose
// route reads it.
type agento11yParamUse struct {
	name       string
	set        bool
	operations []string
}

// unusedBodyField reports the first body field that is set on an operation whose
// route does not send it, with the operations that do read it. Upstream answers
// 200 to a request that never carried the field, so a dropped one reads back as
// a success: a suite created with a test case body looks like it stored the case
// when it holds no version at all.
func (p ManageAgento11yTestSuitesReadWriteParams) unusedBodyField() (string, []string) {
	// A suite and a test case both carry a name, a description, and tags.
	suiteAndCase := []string{"create_suite", "update_suite", "upsert_test_case"}
	caseOnly := []string{"upsert_test_case"}
	draftOnly := []string{"create_draft_version"}

	return agento11yFirstUnusedParam(p.Operation, []agento11yParamUse{
		{name: "name", set: p.Name != nil, operations: suiteAndCase},
		{name: "description", set: p.Description != nil, operations: suiteAndCase},
		{name: "tags", set: p.Tags != nil, operations: suiteAndCase},
		{name: "changelog", set: p.Changelog != "", operations: draftOnly},
		{name: "empty_draft", set: p.EmptyDraft, operations: draftOnly},
		{name: "category", set: p.Category != "", operations: caseOnly},
		{name: "input", set: p.Input != nil, operations: caseOnly},
		{name: "expected", set: p.Expected != nil, operations: caseOnly},
		{name: "metadata", set: p.Metadata != nil, operations: caseOnly},
		{name: "artifact_refs", set: p.ArtifactRefs != nil, operations: caseOnly},
	})
}

// unusedWriteIDField reports an ID that a write operation does not address. It is
// checked on the write operations only: a read that drops one still answers with
// a superset that contains the row, while a write that drops one writes
// something other than what was asked for. 'create_draft_version' rejects a
// version with its own message, so the version it assigns is explained.
func (p ManageAgento11yTestSuitesReadWriteParams) unusedWriteIDField() (string, []string) {
	return agento11yFirstUnusedParam(p.Operation, []agento11yParamUse{
		{name: "version", set: p.Version != "", operations: []string{"publish_version", "upsert_test_case", "delete_test_case"}},
		{name: "test_case_id", set: p.TestCaseID != "", operations: []string{"upsert_test_case", "delete_test_case"}},
	})
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
