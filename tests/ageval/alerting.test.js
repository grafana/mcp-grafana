// k6 `ageval` port of the DeepEval alerting suite (tests/alerting_test.py).
//
// Instead of litellm driving a model against the MCP server (run_llm_tool_loop),
// this runs the REAL `claude` CLI headless with mcp-grafana configured as an MCP
// server, captures the trajectory (tool calls + final answer), and grades it:
//
//   - check()            ~ pytest asserts
//   - res.expectSequence ~ DeepEval MCPUseMetric + assert_tool_operation
//                          (the `input` on each expected tool reproduces the
//                           `operation`/`rule_uid` arg checks via subset match)
//   - judge()            ~ DeepEval GEval (LLM-as-judge against a rubric)
//
// It reuses the same docker-compose Grafana the Python suite uses
// (`make run-test-services` → seeded :3000, admin/admin). The claude-code adapter
// strips the `mcp__grafana__` prefix, so tool names below are the server's own
// names — exactly what alerting_test.py asserts on.
//
// Requires a k6 built with the experimental ageval module, the `claude` CLI
// logged in, and the mcp-grafana binary at ../../dist/mcp-grafana (`make build`).
//
//   make run-test-services
//   make build
//   ANTHROPIC_API_KEY_JUDGE=sk-...  k6 run tests/ageval/alerting.test.js
//
import { CliAgent, judge } from 'k6/experimental/ageval';

const RULES = 'alerting_manage_rules';
const ROUTING = 'alerting_manage_routing';

// One entry per read-only test in alerting_test.py. `expected` feeds
// expectSequence(); `rubric` is the GEval criteria string verbatim.
const CASES = [
  {
    case: 'list-alert-rules',
    prompt: 'List all alert rules in Grafana.',
    expected: [{ name: RULES, input: { operation: 'list' } }],
    rubric:
      'Does the response list alert rules with their titles, states, and labels? ' +
      "There should be at least two rules including 'Test Alert Rule 1' and 'Test Alert Rule 2'.",
  },
  {
    case: 'get-alert-rule-by-uid',
    prompt: "Get the details of the alert rule with UID 'test_alert_rule_1'.",
    expected: [{ name: RULES, input: { operation: 'get', rule_uid: 'test_alert_rule_1' } }],
    rubric:
      'Does the response contain detailed configuration of the alert rule, ' +
      "including its title ('Test Alert Rule 1'), queries, condition, and state?",
  },
  {
    case: 'list-with-label-filter',
    prompt: 'Show me all alert rules that have the label rule=first.',
    expected: [{ name: RULES, input: { operation: 'list' } }],
    rubric:
      'Does the response show filtered alert rules? It should include ' +
      "'Test Alert Rule 1' (which has label rule=first) and should NOT include " +
      "'Test Alert Rule 2' (which has label rule=second).",
  },
  {
    case: 'find-firing-rules',
    prompt: 'Show me all firing alert rules in Grafana',
    expected: [{ name: RULES, input: { operation: 'list' } }],
    rubric:
      'Does the response list only the firing alert rules? ' +
      "It should include 'Test Alert Rule 1' which is firing, " +
      'and should not list rules that are not firing.',
  },
  {
    case: 'get-rule-versions',
    prompt: "Show me the version history for the alert rule with UID 'test_alert_rule_1'.",
    expected: [{ name: RULES, input: { operation: 'versions', rule_uid: 'test_alert_rule_1' } }],
    rubric: 'Does the response contain version history information for the alert rule?',
  },
  {
    case: 'list-rules-in-folder',
    prompt: "What alert rules are in the 'Test Alerts' folder?",
    expected: [{ name: RULES, input: { operation: 'list' } }],
    rubric:
      "Does the response list alert rules from the 'Test Alerts' folder? " +
      "It should include 'Test Alert Rule 1' and 'Test Alert Rule 2'.",
  },
  {
    case: 'get-notification-policies',
    prompt: 'How are alerts routed to receivers in my Grafana instance?',
    expected: [{ name: ROUTING, input: { operation: 'get_notification_policies' } }],
    rubric:
      'Does the response describe the notification policy routing tree? ' +
      'It should mention that alerts with severity=info are routed to Email1, ' +
      "and that the 'weekends' mute time interval is applied.",
  },
  {
    case: 'list-contact-points',
    prompt: 'List all contact points configured in Grafana alerting.',
    expected: [{ name: ROUTING, input: { operation: 'get_contact_points' } }],
    rubric:
      'Does the response list contact points? It should include ' + "'Email1' and 'Email2'.",
  },
  {
    case: 'get-contact-point-by-name',
    prompt: "Show me the details of the contact point named 'Email1' in Grafana alerting.",
    expected: [{ name: ROUTING, input: { operation: 'get_contact_point' } }],
    rubric:
      "Does the response contain details about the 'Email1' contact point, " +
      'including that it is an email type sending to test1@example.com?',
  },
  {
    case: 'list-time-intervals',
    prompt: 'Show me all mute time intervals configured in Grafana alerting.',
    expected: [{ name: ROUTING, input: { operation: 'get_time_intervals' } }],
    rubric:
      'Does the response list time intervals? ' +
      "It should include a 'weekends' interval covering Saturday and Sunday.",
  },
  {
    case: 'get-time-interval-by-name',
    prompt: "Show me the details of the 'weekends' mute time interval in Grafana alerting.",
    expected: [{ name: ROUTING, input: { operation: 'get_time_interval' } }],
    rubric:
      "Does the response describe the 'weekends' time interval, " +
      'including that it covers Saturday and Sunday?',
  },
];

const claude = new CliAgent({
  name: 'claude-code',
  command: 'claude',
  args: [
    '-p',
    '{{input}}',
    '--mcp-config',
    __ENV.MCP_CONFIG || 'tests/ageval/grafana.mcp.json',
    // Pre-approve the two alerting tools so the headless run can call them.
    '--allowedTools',
    `mcp__grafana__${RULES}`,
    `mcp__grafana__${ROUTING}`,
    '--output-format',
    'stream-json',
    '--verbose',
  ],
  format: 'claude-code',
  timeoutSeconds: 180,
});

export const options = {
  // Single VU, single iteration: every case runs once, in order, within one run.
  vus: 1,
  iterations: 1,
  // The k6 equivalent of `assert_test` / the DeepEval thresholds: the run fails
  // if tool-use correctness or judged quality drop below target across all cases.
  // Each metric is also tagged per case, so a single run differentiates them
  // (e.g. `agent_judge_pass{case:list-alert-rules}`).
  thresholds: {
    checks: ['rate>0.9'],
    agent_tool_correctness: ['rate>0.9'],
    agent_quality_score: ['avg>0.7'],
    agent_judge_pass: ['rate>0.9'],
  },
};

// Run every case once inside the single iteration. Each case is tagged with its
// own `case` name and gets exactly one judge() call, so per-case metrics stay
// distinguishable in the summary and in dashboards.
export default function () {
  for (const tc of CASES) {
    const res = claude.run({
      input: tc.prompt,
      expectedTools: tc.expected, // graded by expectSequence() below
      tags: { case: tc.case },
    });

    // assert_tool_operation(...) equivalent: in-order match incl. operation/uid args.
    const correct = res.expectSequence();

    // GEval equivalent: score the trajectory + answer against the rubric.
    // One judge() call per case; `name` tags the metrics with the case.
    const verdict = judge(res, {
      provider: 'anthropic',
      model: 'claude-haiku-4-5',
      apiKey: __ENV.ANTHROPIC_API_KEY_JUDGE || __ENV.ANTHROPIC_API_KEY,
      rubric: tc.rubric,
      threshold: 0.5, // mirrors MCP_EVAL_THRESHOLD in tests/utils.py
      name: tc.case,
    });

    // Diagnostics: a uniform score of 0 almost always means the agent produced
    // no usable trajectory (e.g. the mcp-grafana MCP server failed to launch, so
    // claude had no Grafana tools). Log enough to tell a real low score from an
    // empty run: the judge's own `reason`, the tools actually called, and the
    // answer length.
    console.log(
      `[${tc.case}] toolCorrect=${correct} tools=[${res.toolSequence().join(', ')}] ` +
        `answerChars=${res.output.length} score=${verdict.score} | ${verdict.reason}`
    );
  }
}
