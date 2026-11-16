import { z } from 'zod';

const fields = z.record(z.string(), z.string());
const view = z.object({
  kind: z.literal('logs'),
  query: z.string(),
  exploreUrl: z.string().optional(),
  logs: z.object({
    data: z.array(
      z.object({
        timestamp: z.string().optional(),
        line: z.string().optional(),
        labels: fields,
        structuredMetadata: fields.optional(),
        parsed: fields.optional(),
        value: z.number().optional(),
        values: z.array(z.object({ timestamp: z.string(), value: z.number() })).optional(),
      })
    ),
    metadata: z
      .object({
        linesReturned: z.number().int().nonnegative(),
        resultsTruncated: z.boolean(),
        startTime: z.string().optional(),
        endTime: z.string().optional(),
      })
      .optional(),
    hints: z.object({ summary: z.string() }).optional(),
  }),
});

export type LogsViewResult = z.infer<typeof view>;

export function parseLogsResult(payload: unknown): LogsViewResult | undefined {
  const parsed = view.safeParse(payload);
  return parsed.success ? parsed.data : undefined;
}
