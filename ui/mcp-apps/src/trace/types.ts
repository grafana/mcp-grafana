export type TraceAttributeValue =
  | string
  | number
  | boolean
  | null
  | TraceAttributeValue[]
  | {
      [key: string]: TraceAttributeValue;
    };

export interface TraceEvent {
  name: string;
  timeMs: number;
  attributes: Record<string, TraceAttributeValue>;
}

export type TraceSpanStatus = 'error' | 'ok' | 'unset';

export interface TraceSpan {
  id: string;
  parentId?: string;
  name: string;
  serviceName: string;
  startTimeMs: number;
  durationMs: number;
  status: TraceSpanStatus;
  attributes: Record<string, TraceAttributeValue>;
  events: TraceEvent[];
}

export interface RenderTraceResult {
  traceId: string;
  datasourceUid: string;
  focusSpanId?: string;
  grafanaUrl: string;
  spans: TraceSpan[];
}

export interface TraceNavigationTarget {
  url: string;
}
