import type { MouseEvent, ReactNode } from 'react';
import { useMemo } from 'react';

import { McpAppShell, type McpAppColorMode, type McpAppFeedback } from '../McpAppShell';
import { getTraceSummary, TraceViewer } from './TraceViewer';
import type { TraceViewResult, TraceNavigationTarget } from './types';

export interface RenderTraceAppProps {
  result: TraceViewResult;
  colorMode: McpAppColorMode;
  onOpenInGrafana?: (target: TraceNavigationTarget) => void;
  share?: ReactNode;
  feedback?: McpAppFeedback;
}

function isSafeGrafanaUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function RenderTraceApp({ result, colorMode, onOpenInGrafana, share, feedback }: RenderTraceAppProps) {
  const summary = useMemo(() => getTraceSummary(result), [result]);
  const requestedFocusMissing = Boolean(
    result.focusSpanId && !result.spans.some((span) => span.id === result.focusSpanId)
  );
  const safeGrafanaUrl = isSafeGrafanaUrl(result.grafanaUrl) ? result.grafanaUrl : undefined;
  const openInGrafana = safeGrafanaUrl
    ? {
        href: result.grafanaUrl,
        onClick: onOpenInGrafana
          ? (event: MouseEvent<HTMLAnchorElement>) => {
              event.preventDefault();
              onOpenInGrafana({ url: safeGrafanaUrl });
            }
          : undefined,
      }
    : undefined;

  return (
    <McpAppShell
      product="Grafana"
      scope={`Tempo / ${result.datasourceUid}`}
      colorMode={colorMode}
      layout="wide"
      density="compact"
      summary={{
        label: 'Trace',
        title: summary.title,
        description: (
          <>
            {summary.description}
            <br />
            <code>{result.traceId}</code>
          </>
        ),
      }}
      openInGrafana={openInGrafana}
      share={share}
      feedback={
        feedback ??
        (requestedFocusMissing
          ? {
              tone: 'info',
              message: `Span ${result.focusSpanId} was not found. Showing an available span instead.`,
            }
          : undefined)
      }
    >
      <TraceViewer result={result} />
    </McpAppShell>
  );
}
