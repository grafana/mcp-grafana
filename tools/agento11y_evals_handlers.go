package tools

import (
	"context"
	"errors"
	"fmt"
)

// agento11yEvalsRead dispatches agento11y_evals_read's 30 operations across
// the five merged sub-domains (evaluators, eval rules/guards, saved
// conversations/collections, experiments, test suites). Each sub-domain's
// own Read function already returns errAgento11yUnknownOperation for an
// operation it does not own, so this is the same sentinel-chaining idiom
// Agento11yEvalsReadParams.validate() uses, one level down at dispatch time.
func agento11yEvalsRead(ctx context.Context, args Agento11yEvalsReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_evals_read: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	if result, err := client.agento11yEvaluatorRead(ctx, args.Operation, args.evaluatorReadRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yEvalRuleRead(ctx, args.Operation, args.evalRuleReadRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yEvalCollectionRead(ctx, args.Operation, args.evalCollectionReadRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yExperimentRead(ctx, args.Operation, args.experimentReadRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yTestSuiteRead(ctx, args.Operation, args.testSuiteReadRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}

	// Unreachable once validate() has passed (it recognizes exactly the same
	// operations, via the same five checks); kept for defense in depth.
	return nil, fmt.Errorf("agento11y_evals_read: unknown operation %q", args.Operation)
}

// agento11yEvalsWrite dispatches agento11y_evals_write's 26 operations across
// the same five sub-domains. Unlike agento11yEvalsRead, there is no read
// dispatch to fall through to here: Agento11yEvalsWriteParams.validate() has
// already rejected any operation that isn't one of agento11y_evals_write's
// own, including a sibling read operation, so every operation that reaches
// this switch is known to belong to exactly one of the five write dispatchers
// below.
func agento11yEvalsWrite(ctx context.Context, args Agento11yEvalsWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_evals_write: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	if result, err := client.agento11yEvaluatorWrite(ctx, args.Operation, args.evaluatorWriteRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yEvalRuleWrite(ctx, args.Operation, args.evalRuleWriteRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yEvalCollectionWrite(ctx, args.Operation, args.evalCollectionWriteRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yExperimentWrite(ctx, args.Operation, args.experimentWriteRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}
	if result, err := client.agento11yTestSuiteWrite(ctx, args.Operation, args.testSuiteWriteRequest()); !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}

	// Unreachable once validate() has passed; kept for defense in depth.
	return nil, fmt.Errorf("agento11y_evals_write: unknown operation %q", args.Operation)
}
