import type { ReactNode } from 'react';
import { useMemo } from 'react';

import { openInGrafanaAction } from '../grafanaLink';
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

export function RenderTraceApp({ result, colorMode, onOpenInGrafana, share, feedback }: RenderTraceAppProps) {
  const summary = useMemo(() => getTraceSummary(result), [result]);
  const requestedFocusMissing = Boolean(
    result.focusSpanId && !result.spans.some((span) => span.id === result.focusSpanId)
  );
  const openInGrafana = openInGrafanaAction(result.grafanaUrl, onOpenInGrafana);

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
