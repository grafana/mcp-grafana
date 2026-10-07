import { App } from '@modelcontextprotocol/ext-apps';
import { createRoot } from 'react-dom/client';
import { AppErrorBoundary, installGlobalErrorReporter } from './AppErrorBoundary';
import { MetricsApplication } from './MetricsApplication';
import { createMetricsHostState } from './metricsHostState';

const mount = document.getElementById('root')!;
// Installed before anything else runs: a host iframe has no console the user can
// open, so an unreported error is simply a blank panel.
installGlobalErrorReporter(mount);

const app = new App({ name: 'Grafana metrics', version: '0.1.0' });
// Handlers are attached here, not in an effect: the host can finish the
// handshake and deliver the tool result before React's first commit, and those
// notifications are never replayed.
const host = createMetricsHostState(app);

createRoot(mount).render(
  <AppErrorBoundary>
    <MetricsApplication host={host} />
  </AppErrorBoundary>
);
