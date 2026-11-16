import type { LogsViewResult } from '../src/logs/parseLogsResult';

export const logsArgs = {
  datasourceUid: 'demo-loki',
  logql: '{service_name="checkout"}',
  startRfc3339: '2026-11-13T10:00:00Z',
  endRfc3339: '2026-11-13T10:15:00Z',
  limit: 100,
  direction: 'backward',
};

/** Synthetic data only; the AppBridge harness makes no Grafana requests. */
export function logsResult(refreshes = 0) {
  const payload: LogsViewResult = {
    kind: 'logs',
    query: logsArgs.logql,
    exploreUrl: 'https://example.grafana.net/explore',
    logs: {
      data: [
        {
          timestamp: '2026-11-13T10:14:20.123456789Z',
          line: `Payment provider timed out after 3000ms (refresh ${refreshes})`,
          labels: { service_name: 'checkout', detected_level: 'error' },
          structuredMetadata: { trace_id: 'a4f12d7e', pod: 'checkout-6d49b' },
          parsed: { duration_ms: '3000' },
        },
        {
          timestamp: '2026-11-13T10:14:19.987654321Z',
          line: 'Retrying payment request\nAttempt 2 of 3',
          labels: { service_name: 'checkout', detected_level: 'warn' },
        },
        {
          timestamp: '2026-11-13T10:14:18Z',
          line: 'POST /checkout status=200 duration=42ms',
          labels: { service_name: 'checkout', detected_level: 'info' },
        },
        ...Array.from({ length: 9 }, (_, index) => ({
          timestamp: `2026-11-13T10:14:${String(17 - index).padStart(2, '0')}.436207878Z`,
          line:
            index === 4
              ? 'level=error msg="Payment authorization failed" provider=stripe status=503'
              : `level=info msg="Processed checkout request" order_id=${1042 - index} duration_ms=${42 + index}`,
          labels: { service_name: 'checkout', detected_level: index === 4 ? 'error' : 'info' },
          structuredMetadata: { pod: 'checkout-6d49b', region: 'us-west-2' },
        })),
      ],
      metadata: {
        linesReturned: 12,
        resultsTruncated: false,
        startTime: logsArgs.startRfc3339,
        endTime: logsArgs.endRfc3339,
      },
    },
  };
  return {
    structuredContent: payload,
    content: [{ type: 'text' as const, text: 'Displayed query_loki_logs result in the Grafana App.' }],
    isError: false,
  };
}
