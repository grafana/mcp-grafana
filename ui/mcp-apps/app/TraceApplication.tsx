import { useEffect, useState, useSyncExternalStore } from 'react';
import { Button, LoadingIndicator } from '../src/design';
import { McpAppShell } from '../src/McpAppShell';
import { RenderTraceApp } from '../src/trace/RenderTraceApp';
import type { AppHostState } from './appHostState';

export function TraceApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<string>();
  const result = state.trace;
  useEffect(() => {
    setCopied(false);
    setCopyError(undefined);
  }, [result]);

  if (result) {
    return (
      <RenderTraceApp
        result={result}
        colorMode={state.colorMode}
        feedback={copyError ? { tone: 'error', message: copyError } : state.notice}
        share={result.grafanaUrl ? (
          <Button
            variant="ghost"
            size="xs"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(result.grafanaUrl);
                setCopied(true);
                setCopyError(undefined);
              } catch {
                setCopyError('Could not copy the trace link. Use Open in Grafana to share it from Grafana.');
              }
            }}
          >
            {copied ? 'Link copied' : 'Share'}
          </Button>
        ) : undefined}
        onOpenInGrafana={({ url }) => { if (url) host.openInGrafana({ url }); }}
      />
    );
  }
  return (
    <McpAppShell
      product="Grafana"
      colorMode={state.colorMode}
      layout="wide"
      density="compact"
      summary={{ label: 'Trace', title: state.isError ? 'Trace unavailable' : 'Loading trace…' }}
      feedback={state.isError ? { tone: 'error', message: state.status } : undefined}
    >
      {!state.isError && <div role="status" aria-label="Loading trace"><LoadingIndicator /></div>}
    </McpAppShell>
  );
}
