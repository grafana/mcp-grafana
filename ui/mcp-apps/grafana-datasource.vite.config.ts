import { appBuildConfig } from './app.vite.config';

// The App with the Grafana ECharts renderer, for the any-datasource view.
export default appBuildConfig('app-datasource.html', { renderer: true });
