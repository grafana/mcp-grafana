import type { MouseEvent, ReactNode } from 'react';

import { McpAppShell, type McpAppColorMode, type McpAppFeedback } from '../McpAppShell';
import { getTraceSummary, TraceViewer } from './TraceViewer';
import type { RenderTraceResult, TraceNavigationTarget } from './types';

export interface RenderTraceAppProps {
  result: RenderTraceResult;
  colorMode: McpAppColorMode;
  onOpenInGrafana?: (target: TraceNavigationTarget) => void;
  share?: ReactNode;
  feedback?: McpAppFeedback;
}

export function RenderTraceApp({ result, colorMode, onOpenInGrafana, share, feedback }: RenderTraceAppProps) {
  const summary = getTraceSummary(result);
  const requestedFocusMissing = Boolean(
    result.focusSpanId && !result.spans.some((span) => span.id === result.focusSpanId)
  );
  const openInGrafana = result.grafanaUrl
    ? {
        href: result.grafanaUrl,
        onClick: onOpenInGrafana
          ? (event: MouseEvent<HTMLAnchorElement>) => {
              event.preventDefault();
              onOpenInGrafana({ url: result.grafanaUrl });
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
