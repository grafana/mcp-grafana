import type { App } from '@modelcontextprotocol/ext-apps';
import { useState } from 'react';

import { GrafanaApplication } from '../app/GrafanaApplication';
import { createAppHostState } from '../app/appHostState';
// The same script the server serves as the Renderer resource. Committed, so no download is needed.
import rendererScript from '../dist/viz-renderer.js?raw';

/** What a host sends the App for one `render_grafana_app` call. */
export type Scenario = {
  toolName: 'query_prometheus' | 'get_tempo_trace' | 'query_datasource';
  /** The arguments of the source tool call. */
  args: Record<string, unknown>;
  /** The structuredContent of the tool result. Leave out to stay in the waiting state. */
  result?: unknown;
  /** A tool result with isError set. */
  failed?: boolean;
  cancelled?: boolean;
  /** The host closes the App. */
  closed?: boolean;
  /** Whether the host lets the App call server tools (refresh) and read server resources (the Renderer). */
  serverTools?: boolean;
  serverResources?: boolean;
};

/** A stand-in for the MCP host: the same App surface the Vitest tests fake, plus the Renderer resource. */
function fakeApp(colorMode: 'light' | 'dark', s: Scenario): App {
  return {
    connect: async () => {},
    close: async () => {},
    getHostContext: () => ({ theme: colorMode }),
    getHostCapabilities: () => ({
      updateModelContext: {},
      openLinks: {},
      ...(s.serverTools === false ? {} : { serverTools: {} }),
      ...(s.serverResources === false ? {} : { serverResources: {} }),
    }),
    // A refresh answers with the same result, so the Refresh button works.
    callServerTool: async () => ({ content: [], structuredContent: s.result }),
    readServerResource: async ({ uri }: { uri: string }) => ({
      contents: [{ uri, mimeType: 'text/javascript', text: rendererScript }],
    }),
    updateModelContext: async () => ({}),
    openLink: async () => ({}),
    sendMessage: async () => ({}),
  } as unknown as App;
}

function startHost(colorMode: 'light' | 'dark', s: Scenario) {
  const app = fakeApp(colorMode, s);
  // Handlers attach inside createAppHostState, before any React render, like app/app.tsx.
  const host = createAppHostState(app);
  app.ontoolinput?.({ arguments: { toolName: s.toolName, arguments: s.args } } as never);
  if (s.closed) void app.onteardown?.({} as never, {} as never);
  else if (s.cancelled) app.ontoolcancelled?.({} as never);
  else if (s.failed) app.ontoolresult?.({ isError: true, content: [{ type: 'text', text: 'error' }] } as never);
  else if (s.result !== undefined) app.ontoolresult?.({ structuredContent: s.result, content: [] } as never);
  return host;
}

/** The real App (GrafanaApplication) fed by a scripted host. Remount the story to replay it. */
export function HostedApp({ scenario, colorMode }: { scenario: Scenario; colorMode: 'light' | 'dark' }) {
  const [host] = useState(() => startHost(colorMode, scenario));
  return <GrafanaApplication host={host} />;
}
