import { App } from '@modelcontextprotocol/ext-apps';
import { createRoot } from 'react-dom/client';
import { GrafanaApplication } from './GrafanaApplication';
import { createAppHostState } from './appHostState';

const app = new App({ name: 'Grafana App', version: '0.1.0' });
// Handlers are attached here, not in an effect: the host can finish the
// handshake and deliver the tool result before React's first commit, and those
// notifications are never replayed.
const host = createAppHostState(app);

createRoot(document.getElementById('root')!).render(<GrafanaApplication host={host} />);
