import { App } from '@modelcontextprotocol/ext-apps';
import { createRoot } from 'react-dom/client';
import { ServiceHealthApplication } from './ServiceHealthApplication';

const app = new App({ name: 'Grafana service health', version: '0.1.0' });
createRoot(document.getElementById('root')!).render(<ServiceHealthApplication app={app} />);
