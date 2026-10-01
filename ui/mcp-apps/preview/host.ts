import { AppBridge, PostMessageTransport } from '@modelcontextprotocol/ext-apps/app-bridge';
import traceAppHTML from '../dist/trace.html?raw';
import { trace } from './traceFixture';

const frame = document.querySelector<HTMLIFrameElement>('#trace-app');
const frameWrap = document.querySelector<HTMLElement>('#frame-wrap');
const status = document.querySelector<HTMLElement>('#status');

if (!frame || !frameWrap || !status) {
  throw new Error('Trace host harness elements are missing.');
}

frame.srcdoc = traceAppHTML;

const bridge = new AppBridge(
  null,
  { name: 'Grafana MCP App harness', version: '0.1.0' },
  { openLinks: {} },
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

bridge.oninitialized = async () => {
  await bridge.sendToolInput({
    arguments: {
      trace_id: trace.traceId,
      datasource_uid: trace.datasourceUid,
      focus_span_id: trace.focusSpanId,
    },
  });
  await bridge.sendToolResult({
    content: [{ type: 'text', text: `Displaying trace ${trace.traceId}.` }],
    structuredContent: trace,
  });
  status.textContent = 'Connected through AppBridge; tool input and result delivered.';
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
