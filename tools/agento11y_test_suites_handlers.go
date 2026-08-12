package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

func agento11yTestSuitePath(suiteID string) string {
	return "/eval/test-suites/" + url.PathEscape(suiteID)
}

func agento11yTestSuiteVersionPath(suiteID, version string) string {
	return agento11yTestSuitePath(suiteID) + "/versions/" + url.PathEscape(version)
}

// agento11yTestCasesPath builds the path of the test cases of one version. A
// case belongs to a version, so there is no suite-wide case route.
func agento11yTestCasesPath(suiteID, version string) string {
	return agento11yTestSuiteVersionPath(suiteID, version) + "/test-cases"
}

func (c *Client) listAgento11yTestSuites(ctx context.Context, limit int, cursor string) (*agento11yListResponse[Agento11yTestSuite], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yTestSuite]](ctx, c, http.MethodGet, "/eval/test-suites", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yTestSuite(ctx context.Context, suiteID string) (*Agento11yTestSuite, error) {
	resp, err := fetchAgento11yJSON[Agento11yTestSuite](ctx, c, http.MethodGet, agento11yTestSuitePath(suiteID), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yTestCases(ctx context.Context, suiteID, version string, limit int, cursor string) (*agento11yListResponse[Agento11yTestCase], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yTestCase]](ctx, c, http.MethodGet, agento11yTestCasesPath(suiteID, version), agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yTestCase(ctx context.Context, suiteID, version, testCaseID string) (*Agento11yTestCase, error) {
	resp, err := fetchAgento11yJSON[Agento11yTestCase](ctx, c, http.MethodGet, agento11yTestCasesPath(suiteID, version)+"/"+url.PathEscape(testCaseID), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yTestSuiteRead runs the test suite read operations shared by the read
// and read-write variants of agento11y_manage_test_suites. Operations it does
// not handle return errAgento11yUnknownOperation.
func (c *Client) agento11yTestSuiteRead(ctx context.Context, operation string, r agento11yTestSuiteReadRequest) (any, error) {
	switch operation {
	case "list_suites":
		return c.listAgento11yTestSuites(ctx, r.Limit, r.Cursor)
	case "get_suite":
		return c.getAgento11yTestSuite(ctx, r.SuiteID)
	case "list_test_cases":
		return c.listAgento11yTestCases(ctx, r.SuiteID, r.Version, r.Limit, r.Cursor)
	case "get_test_case":
		return c.getAgento11yTestCase(ctx, r.SuiteID, r.Version, r.TestCaseID)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yTestSuitesRead(ctx context.Context, args ManageAgento11yTestSuitesReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_test_suites: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yTestSuiteRead(ctx, args.Operation, args.readRequest())
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_test_suites: unknown operation %q", args.Operation)
	}
	return result, err
}

// createAgento11yTestSuite creates a suite. The API rejects a body field it
// does not know, so an unset parameter is left out instead of sent as a zero
// value. A new suite has no version: create_draft_version opens the first one.
func (c *Client) createAgento11yTestSuite(ctx context.Context, p ManageAgento11yTestSuitesReadWriteParams) (*Agento11yTestSuite, error) {
	body := map[string]any{}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.SuiteID != "" {
		body["suite_id"] = p.SuiteID
	}
	if p.Description != nil {
		body["description"] = *p.Description
	}
	if p.Tags != nil {
		body["tags"] = *p.Tags
	}

	resp, err := fetchAgento11yJSON[Agento11yTestSuite](ctx, c, http.MethodPost, "/eval/test-suites", nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateAgento11yTestSuite patches a suite. Only the supplied fields are sent,
// because the API reads them as optional pointers: an absent field is left
// unchanged while an explicitly empty description or tag list clears it. The
// response carries the version history, like get_suite.
func (c *Client) updateAgento11yTestSuite(ctx context.Context, p ManageAgento11yTestSuitesReadWriteParams) (*Agento11yTestSuite, error) {
	body := map[string]any{}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.Description != nil {
		body["description"] = *p.Description
	}
	if p.Tags != nil {
		body["tags"] = *p.Tags
	}

	resp, err := fetchAgento11yJSON[Agento11yTestSuite](ctx, c, http.MethodPatch, agento11yTestSuitePath(p.SuiteID), nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// createAgento11yTestSuiteVersion opens a new editable version. The version
// string is assigned upstream, so it is not sent. By default every test case of
// the latest published version is copied into the new one; empty_draft starts
// from nothing instead.
func (c *Client) createAgento11yTestSuiteVersion(ctx context.Context, p ManageAgento11yTestSuitesReadWriteParams) (*Agento11yTestSuiteVersion, error) {
	body := map[string]any{"empty_draft": p.EmptyDraft}
	if p.Changelog != "" {
		body["changelog"] = p.Changelog
	}

	resp, err := fetchAgento11yJSON[Agento11yTestSuiteVersion](ctx, c, http.MethodPost, agento11yTestSuitePath(p.SuiteID)+"/versions", nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// publishAgento11yTestSuiteVersion freezes a draft version and makes it the
// suite's latest_version. Upstream splits the last path segment on a colon to
// read the action, and url.PathEscape leaves a colon alone, so the action is
// appended after the version is escaped and never comes from the caller. The
// route takes no body.
func (c *Client) publishAgento11yTestSuiteVersion(ctx context.Context, suiteID, version string) (*Agento11yTestSuiteVersion, error) {
	resp, err := fetchAgento11yJSON[Agento11yTestSuiteVersion](ctx, c, http.MethodPost, agento11yTestSuiteVersionPath(suiteID, version)+":publish", nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// upsertAgento11yTestCase writes a whole test case into a draft version. The
// route replaces the stored case rather than merging into it, so a field left
// out here is cleared on an existing case.
func (c *Client) upsertAgento11yTestCase(ctx context.Context, p ManageAgento11yTestSuitesReadWriteParams) (*Agento11yTestCase, error) {
	body := map[string]any{"input": p.Input}
	if p.TestCaseID != "" {
		body["test_case_id"] = p.TestCaseID
	}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.Description != nil {
		body["description"] = *p.Description
	}
	if p.Tags != nil {
		body["tags"] = *p.Tags
	}
	if p.Category != "" {
		body["category"] = p.Category
	}
	if p.Expected != nil {
		body["expected"] = p.Expected
	}
	if p.Metadata != nil {
		body["metadata"] = p.Metadata
	}
	if p.ArtifactRefs != nil {
		body["artifact_refs"] = p.ArtifactRefs
	}

	resp, err := fetchAgento11yJSON[Agento11yTestCase](ctx, c, http.MethodPost, agento11yTestCasesPath(p.SuiteID, p.Version), nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteAgento11yTestCase removes one case from a draft version. The API
// answers 204, and 404 for a case that is already gone.
func (c *Client) deleteAgento11yTestCase(ctx context.Context, suiteID, version, testCaseID string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, agento11yTestCasesPath(suiteID, version)+"/"+url.PathEscape(testCaseID), nil, nil)
	return err
}

// agento11yTestSuiteWrite runs the write operations of
// agento11y_manage_test_suites. Operations it does not handle return
// errAgento11yUnknownOperation.
func (c *Client) agento11yTestSuiteWrite(ctx context.Context, operation string, p ManageAgento11yTestSuitesReadWriteParams) (any, error) {
	switch operation {
	case "create_suite":
		return c.createAgento11yTestSuite(ctx, p)
	case "update_suite":
		return c.updateAgento11yTestSuite(ctx, p)
	case "create_draft_version":
		return c.createAgento11yTestSuiteVersion(ctx, p)
	case "publish_version":
		return c.publishAgento11yTestSuiteVersion(ctx, p.SuiteID, p.Version)
	case "upsert_test_case":
		return c.upsertAgento11yTestCase(ctx, p)
	case "delete_test_case":
		if err := c.deleteAgento11yTestCase(ctx, p.SuiteID, p.Version, p.TestCaseID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Test case %s deleted from version %s of test suite %s successfully", p.TestCaseID, p.Version, p.SuiteID), nil
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yTestSuitesReadWrite(ctx context.Context, args ManageAgento11yTestSuitesReadWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_test_suites: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yTestSuiteRead(ctx, args.Operation, args.readRequest())
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}

	result, err = client.agento11yTestSuiteWrite(ctx, args.Operation, args)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_test_suites: unknown operation %q", args.Operation)
	}
	return result, err
}
