import type { RenderTraceResult } from '../src/trace/types';

const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);

/** Validate the server/iframe boundary before rendering any trace fields. */
export function parseTraceResult(value: unknown): RenderTraceResult | undefined {
  if (
    !object(value) ||
    typeof value.traceId !== 'string' ||
    typeof value.datasourceUid !== 'string' ||
    !Array.isArray(value.spans)
  ) {
    return undefined;
  }
  if (value.focusSpanId !== undefined && typeof value.focusSpanId !== 'string') {
    return undefined;
  }
  if (typeof value.grafanaUrl !== 'string') {
    return undefined;
  }
  if (value.grafanaUrl) {
    try {
      const url = new URL(value.grafanaUrl);
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) {
        return undefined;
      }
    } catch {
      return undefined;
    }
  }
  const ids = new Set<string>();
  for (const span of value.spans) {
    if (
      !object(span) ||
      typeof span.id !== 'string' ||
      !span.id ||
      ids.has(span.id) ||
      (span.parentId !== undefined && typeof span.parentId !== 'string') ||
      typeof span.name !== 'string' ||
      typeof span.serviceName !== 'string' ||
      !finite(span.startTimeMs) ||
      !finite(span.durationMs) ||
      span.durationMs < 0 ||
      typeof span.status !== 'string' ||
      !['ok', 'error', 'unset'].includes(span.status) ||
      !object(span.attributes) ||
      !Array.isArray(span.events)
    ) {
      return undefined;
    }
    ids.add(span.id);
    for (const event of span.events) {
      if (!object(event) || typeof event.name !== 'string' || !finite(event.timeMs) || !object(event.attributes)) {
        return undefined;
      }
    }
  }
  return value as unknown as RenderTraceResult;
}
