import { AppBridge, PostMessageTransport } from '@modelcontextprotocol/ext-apps/app-bridge';
import appHTML from '../dist/service-health.html?raw';
import { makeServiceHealthFixture } from './serviceHealthFixture';
import type { ServiceHealthTimeRange } from '../src/service-health/types';

const frame = document.querySelector<HTMLIFrameElement>('#service-health-app');
const frameWrap = document.querySelector<HTMLElement>('#frame-wrap');
const status = document.querySelector<HTMLElement>('#status');

if (!frame || !frameWrap || !status) {
  throw new Error('Service health host harness elements are missing.');
}

frame.srcdoc = appHTML;

const bridge = new AppBridge(
  null,
  { name: 'Grafana MCP App harness', version: '0.1.0' },
  { openLinks: {}, serverTools: {} },
  {
    hostContext: {
      theme: 'light',
      displayMode: 'inline',
      availableDisplayModes: ['inline'],
      locale: 'en-US',
      platform: 'web',
    },
  }
);

bridge.onerror = (error) => {
  status.textContent = `MCP bridge error: ${error.message}`;
};

bridge.onopenlink = async ({ url }) => {
  status.textContent = `Open in Grafana requested: ${url}`;
  return {};
};

const toolResult = (timeRange: ServiceHealthTimeRange) => ({
  content: [{ type: 'text' as const, text: 'Synthetic service health preview.' }],
  structuredContent: makeServiceHealthFixture(timeRange),
});
bridge.oncalltool = async ({ name, arguments: args }) => {
  if (name !== 'render_service_health' || !['1h', '6h', '24h', '7d'].includes(String(args?.time_range))) {
    return { isError: true, content: [{ type: 'text', text: 'Unsupported preview request.' }] };
  }
  status.textContent = `Synthetic preview refreshed: ${args?.time_range}.`;
  return toolResult(args?.time_range as ServiceHealthTimeRange);
};
bridge.oninitialized = async () => {
  await bridge.sendToolInput({
    arguments: { service_name: 'checkout-service', datasource_uid: 'prometheus-demo', time_range: '1h' },
  });
  await bridge.sendToolResult(toolResult('1h'));
  status.textContent = 'Synthetic data · connected through the MCP AppBridge.';
};

const transport = new PostMessageTransport(frame.contentWindow!, frame.contentWindow!);
await bridge.connect(transport);

for (const button of document.querySelectorAll<HTMLButtonElement>('[data-theme]')) {
  button.addEventListener('click', () => {
    const theme = button.dataset.theme === 'dark' ? 'dark' : 'light';
    bridge.setHostContext({ theme, displayMode: 'inline', availableDisplayModes: ['inline'] });
    for (const candidate of document.querySelectorAll<HTMLButtonElement>('[data-theme]')) {
      candidate.setAttribute('aria-pressed', String(candidate === button));
    }
  });
}

for (const button of document.querySelectorAll<HTMLButtonElement>('[data-width]')) {
  button.addEventListener('click', () => {
    const width = button.dataset.width;
    frameWrap.style.maxWidth = width === 'full' ? 'none' : `${width}px`;
    for (const candidate of document.querySelectorAll<HTMLButtonElement>('[data-width]')) {
      candidate.setAttribute('aria-pressed', String(candidate === button));
    }
  });
}
