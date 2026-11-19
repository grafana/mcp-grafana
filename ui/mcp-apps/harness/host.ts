import { AppBridge, PostMessageTransport } from '@modelcontextprotocol/ext-apps/app-bridge';
import { deliverScenario, refreshResult, chartNames } from './scenarios';

const params = new URLSearchParams(location.search);
const scenario = params.get('scenario') ?? 'viz';
const scenarioSelect = document.querySelector<HTMLSelectElement>('#scenario')!;
for (const chart of chartNames) scenarioSelect.add(new Option(chart, `chart:${chart}`));
scenarioSelect.value = params.has('chart') ? `chart:${params.get('chart')}` : scenario;
scenarioSelect.addEventListener('change', () => { const value = scenarioSelect.value;
  location.search = new URLSearchParams(value.startsWith('chart:') ? { chart: value.slice(6) } : { scenario: value }).toString(); });
const frame = document.querySelector<HTMLIFrameElement>('#app')!;
const log = document.querySelector('#log')!;
const record = (event: string, value: unknown = '') => { log.textContent += `${event} ${JSON.stringify(value)}\n`; };
const read = async (uri: string) => {
  const response = await fetch(`/mcp/resource?uri=${encodeURIComponent(uri)}`);
  if (!response.ok) throw new Error(await response.text());
  return response.json();
};
const catalog = await (await fetch('/mcp/tools')).json();
const allowed = new Set(catalog.tools.map((tool: any) => tool._meta?.ui?.resourceUri));
const uri = scenario.startsWith('panel') ? 'ui://mcp-grafana/panel-viewer.html' : 'ui://grafana/app.html';
const resource = await read(uri);
const content = resource.contents[0];
const csp = content._meta?.ui?.csp;
const permissions = content._meta?.ui?.permissions;
// Upstream basic-host owns origin isolation, the inner iframe, and HTTP CSP.
frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms');
const strict = params.get('sandbox') === 'strict';
const proxyReady = strict ? Promise.resolve() : new Promise<void>(resolve => {
  const listener = (event: MessageEvent) => {
    if (event.source === frame.contentWindow && event.data?.method === 'ui/notifications/sandbox-proxy-ready') {
      window.removeEventListener('message', listener); resolve();
    }
  };
  window.addEventListener('message', listener);
});
const sandboxUrl = new URL('http://localhost:5197/sandbox.html');
if (csp) sandboxUrl.searchParams.set('csp', JSON.stringify(csp));
if (!strict) {
  frame.src = sandboxUrl.href;
  await proxyReady;
}
const bridge = new AppBridge(null, { name: 'browser-contract-host', version: '1.0.0' },
  { serverResources: {}, serverTools: {}, openLinks: {}, updateModelContext: { text: {} } },
  { hostContext: { theme: params.get('mode') === 'dark' ? 'dark' : 'light', locale: 'en-US', platform: 'web' } });
bridge.onerror = error => record('bridge-error', error.message);
bridge.onreadresource = async ({ uri }) => {
  record('resource-request', uri);
  if (params.get('policy') === 'tool-declared' && !allowed.has(uri)) {
    record('resource-rejected', uri);
    throw new Error('Resource unavailable: read_failed');
  }
  record('resource-forwarded', uri);
  return read(uri);
};
bridge.onupdatemodelcontext = async value => { record('model-context', value); return {}; };
bridge.onopenlink = async value => { record('open-link', value); return {}; };
bridge.onmessage = async value => { record('message', value); return {}; };
bridge.onsizechange = value => record('size-change', value);
const callGo = async (value: { name: string; arguments?: Record<string, unknown> }) => {
  record('tool-call', value);
  const response = await fetch('/mcp/call', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) });
  if (!response.ok) throw new Error(await response.text());
  const { result, requests } = await response.json();
  record('grafana-requests', requests);
  record('go-result', result);
  return result;
};
let refreshAttempts = 0;
bridge.oncalltool = async value => {
  if (scenario === 'go') return callGo(value);
  record('tool-call', value);
  if (params.get('refresh') === 'fail' || (params.get('refresh') === 'fail-once' && refreshAttempts++ === 0)) return { content: [{ type: 'text', text: 'The data source did not answer.' }], isError: true };
  return refreshResult(value.name);
};
const ready = new Promise<void>(resolve => { bridge.oninitialized = () => { record('initialized'); resolve(); }; });
await bridge.connect(new PostMessageTransport(frame.contentWindow!, frame.contentWindow!));
if (strict) {
  // basic-host's relay requires its own origin; opaque-origin Apps connect directly.
  frame.setAttribute('sandbox', 'allow-scripts');
  frame.src = '/mcp/strict-app';
} else {
  await bridge.sendSandboxResourceReady({ html: content.text, csp, permissions });
}
await ready;
let dark = params.get('mode') === 'dark';
document.querySelector('#theme')!.addEventListener('click', () => {
  dark = !dark; bridge.setHostContext({ theme: dark ? 'dark' : 'light' });
});
document.querySelector('#resize')!.addEventListener('click', () => { frame.style.width = '520px'; });
await deliverScenario(bridge, params, callGo);
record('result-delivered', scenario);
