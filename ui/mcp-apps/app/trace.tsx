import { App } from '@modelcontextprotocol/ext-apps';
import { createRoot } from 'react-dom/client';
import { TraceApplication } from './TraceApplication';

const app = new App({ name: 'Grafana trace', version: '0.1.0' });
createRoot(document.getElementById('root')!).render(<TraceApplication app={app} />);
