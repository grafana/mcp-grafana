import { Button } from '../src/design';
import { LoadingIndicator } from '../src/design';
import type { App } from '@modelcontextprotocol/ext-apps';
import { useEffect, useRef, useState } from 'react';
import { McpAppShell, type McpAppColorMode } from '../src/McpAppShell';
import { RenderServiceHealthApp } from '../src/service-health/RenderServiceHealthApp';
import type { RenderServiceHealthResult, ServiceHealthTimeRange } from '../src/service-health/types';
import { parseServiceHealthResult } from './parseServiceHealthResult';
import '../styles.css';

export function ServiceHealthApplication({ app }: { app: App }) {
  const [result, setResult] = useState<RenderServiceHealthResult>();
  const [colorMode, setColorMode] = useState<McpAppColorMode>('light');
  const [error, setError] = useState<string>();
  const [pending, setPending] = useState(false);
  const [copied, setCopied] = useState(false);
  const request = useRef(0);
  const busy = useRef(false);

  useEffect(() => {
    app.ontoolresult = (response) => {
      request.current++;
      busy.current = false;
      setPending(false);
      const next = !response.isError && parseServiceHealthResult(response.structuredContent);
      if (!next) {
        setError(
          'Could not load service health. Check the service and datasource access, then run render_service_health again.'
        );
        return;
      }
      setResult(next);
      setError(undefined);
      setCopied(false);
    };
    app.ontoolcancelled = () => {
      request.current++;
      busy.current = false;
      setPending(false);
      setError('Service health loading was cancelled.');
    };
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
      request.current++;
      setResult(undefined);
      setError('Service health app closed.');
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
      .catch(() => setError('Could not connect to the MCP host. Reopen the service health app to try again.'));
    return () => {
      request.current++;
      void app.close();
    };
  }, [app]);

  const changeRange = async (timeRange: ServiceHealthTimeRange) => {
    if (!result || busy.current) {
      return;
    }
    busy.current = true;
    const id = ++request.current;
    setPending(true);
    setError(undefined);
    try {
      const response = await app.callServerTool({
        name: 'render_service_health',
        arguments: {
          service_name: result.serviceName,
          datasource_uid: result.datasourceUid,
          ...(result.serviceNamespace ? { service_namespace: result.serviceNamespace } : {}),
          time_range: timeRange,
        },
      });
      if (id !== request.current) {
        return;
      }
      const next = !response.isError && parseServiceHealthResult(response.structuredContent);
      if (
        !next ||
        next.timeRange !== timeRange ||
        next.serviceName !== result.serviceName ||
        next.datasourceUid !== result.datasourceUid ||
        next.serviceNamespace !== result.serviceNamespace
      ) {
        throw new Error('Unexpected service health result');
      }
      setResult(next);
      setCopied(false);
    } catch {
      if (id === request.current) {
        setError('Could not update the time range. Previous results are still shown. Try the range again.');
      }
    } finally {
      if (id === request.current) {
        busy.current = false;
        setPending(false);
      }
    }
  };

  const navigate = async (url: string) => {
    try {
      const response = await app.openLink({ url });
      if (response.isError) {
        throw new Error('Link rejected');
      }
    } catch {
      setError('Could not open Grafana. Try the link again.');
    }
  };

  if (!result) {
    return (
      <McpAppShell
        product="Grafana Cloud"
        colorMode={colorMode}
        summary={{ label: 'Service health', title: error ? 'Service health unavailable' : 'Loading service health…' }}
        feedback={error ? { tone: 'error', message: error } : undefined}
      >
        {!error && (
          <div role="status" aria-label="Loading service health">
            <LoadingIndicator />
          </div>
        )}
      </McpAppShell>
    );
  }
  return (
    <RenderServiceHealthApp
      result={result}
      colorMode={colorMode}
      pending={pending}
      onTimeRangeChange={changeRange}
      onNavigate={navigate}
      feedback={error ? { tone: 'error', message: error } : undefined}
      share={
        result.grafanaUrl ? (
          <Button
            variant="ghost"
            size="xs"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(result.grafanaUrl!);
                setCopied(true);
              } catch {
                setError('Could not copy the service link. Use Open in Grafana to share it from Grafana.');
              }
            }}
          >
            {copied ? 'Link copied' : 'Share'}
          </Button>
        ) : undefined
      }
    />
  );
}
