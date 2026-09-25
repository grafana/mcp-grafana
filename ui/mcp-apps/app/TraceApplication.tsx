import type { App } from '@modelcontextprotocol/ext-apps';
import { Button } from '../src/design';
import { LoadingIndicator } from '../src/design';
import { useEffect, useState } from 'react';
import { McpAppShell, type McpAppColorMode } from '../src/McpAppShell';
import { RenderTraceApp } from '../src/trace/RenderTraceApp';
import type { RenderTraceResult } from '../src/trace/types';
import '../styles.css';
import { parseTraceResult } from './parseTraceResult';

export function TraceApplication({ app }: { app: App }) {
  const [result, setResult] = useState<RenderTraceResult>();
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string>();
  const [colorMode, setColorMode] = useState<McpAppColorMode>('light');

  useEffect(() => {
    app.ontoolresult = (response) => {
      if (response.isError) {
        setError('Could not load the trace. Check the trace ID and datasource access, then run render_trace again.');
        return;
      }
      const value = parseTraceResult(response.structuredContent);
      if (!value) {
        setError('The server returned an unsupported trace response.');
        return;
      }
      setResult(value);
      setCopied(false);
      setError(undefined);
    };
    app.ontoolcancelled = () => setError('Trace loading was cancelled.');
    app.onhostcontextchanged = (context) => {
      if (context.theme) {
        setColorMode(context.theme);
      }
      if (context.safeAreaInsets) {
        const { top, right, bottom, left } = context.safeAreaInsets;
        document.body.style.padding = `${top}px ${right}px ${bottom}px ${left}px`;
        document.body.style.boxSizing = 'border-box';
      }
    };
    app.onteardown = async () => {
      setResult(undefined);
      setError('Trace app closed.');
      return {};
    };
    app
      .connect()
      .then(() => {
        const context = app.getHostContext();
        if (context) {
          app.onhostcontextchanged?.(context);
        }
      })
      .catch(() => setError('Could not connect to the MCP host. Reopen the trace app to try again.'));
    return () => {
      void app.close();
    };
  }, [app]);

  if (result) {
    return (
      <>
        <RenderTraceApp
          result={result}
          colorMode={colorMode}
          feedback={error ? { tone: 'error', message: error } : undefined}
          share={result.grafanaUrl ? (
            <Button
              variant="ghost"
              size="xs"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(result.grafanaUrl);
                  setCopied(true);
                  setError(undefined);
                } catch {
                  setError('Could not copy the trace link. Use Open in Grafana to share it from Grafana.');
                }
              }}
            >
              {copied ? 'Link copied' : 'Share'}
            </Button>
          ) : undefined}
          onOpenInGrafana={async ({ url }) => {
            if (!url) {
              return;
            }
            try {
              const response = await app.openLink({ url });
              if (response.isError) {
                setError('The host could not open Grafana. Try the link again.');
              } else {
                setError(undefined);
              }
            } catch {
              setError('Could not open Grafana. Try the link again.');
            }
          }}
        />
      </>
    );
  }
  return (
    <McpAppShell
      product="Grafana"
      colorMode={colorMode}
      layout="wide"
      density="compact"
      summary={{ label: 'Trace', title: error ? 'Trace unavailable' : 'Loading trace…' }}
      feedback={error ? { tone: 'error', message: error } : undefined}
    >
      {!error && (
        <div role="status" aria-label="Loading trace">
          <LoadingIndicator />
        </div>
      )}
    </McpAppShell>
  );
}
