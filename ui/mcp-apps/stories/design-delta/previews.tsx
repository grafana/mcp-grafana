import { parsePrometheusResult } from '../../app/parsePrometheusResult';
import { McpAppSection } from '../../src/McpAppSection';
import { McpAppShell, type McpAppColorMode } from '../../src/McpAppShell';
import { RenderMetricsApp } from '../../src/metrics/RenderMetricsApp';
import { matrixMultiSeries } from '../../test/metricsFixtures';

type P = { colorMode: McpAppColorMode };

const parse = (payload: unknown) => {
  const parsed = parsePrometheusResult(payload);
  if (!parsed) throw new Error('fixture did not parse');
  return parsed;
};

const body = <p style={{ margin: 0 }}>The view for the tool result goes here.</p>;
const summary = { label: 'Data source query', title: 'sum(rate(http_requests_total[5m]))', description: 'Time series · prometheus' };

export const ShellPreview = ({ colorMode }: P) => (
  <McpAppShell
    product="Grafana"
    colorMode={colorMode}
    summary={summary}
    openInGrafana={{ href: 'https://example.grafana.net/explore', onClick: (e) => e.preventDefault() }}
  >
    {body}
  </McpAppShell>
);

export const SummaryPreview = ({ colorMode }: P) => (
  <McpAppShell
    product="Grafana"
    colorMode={colorMode}
    summary={{ label: 'Prometheus query', title: 'Latency SLO breached', description: 'p95 latency is 3.2 s, over the 500 ms objective.' }}
  >
    {body}
  </McpAppShell>
);

export const BlocksPreview = ({ colorMode }: P) => (
  <McpAppShell product="Grafana" colorMode={colorMode} summary={summary}>
    <McpAppSection title="Interactive surface" description="A section inside the shell.">
      {body}
    </McpAppSection>
  </McpAppShell>
);

export const AlertsPreview = ({ colorMode }: P) => (
  <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
    {(['error', 'success', 'info'] as const).map((tone) => (
      <McpAppShell
        key={tone}
        product="Grafana"
        colorMode={colorMode}
        summary={{ label: 'Alerts', title: `${tone === 'success' ? 'A' : 'An'} ${tone} alert` }}
        feedback={{ tone, message: `This is the ${tone} feedback banner. The design adds a warning tone, a title and a close button.`, action: tone === 'error' ? { label: 'Retry', onClick: () => {} } : undefined }}
      >
        {body}
      </McpAppShell>
    ))}
  </div>
);

export const ButtonsPreview = ({ colorMode }: P) => (
  <McpAppShell
    product="Grafana"
    colorMode={colorMode}
    summary={summary}
    primaryAction={{ label: 'Investigate cause', onClick: () => {} }}
    secondaryAction={{ label: 'Compare with last week', onClick: () => {} }}
  >
    {body}
  </McpAppShell>
);

export const QueryPreview = ({ colorMode }: P) => (
  <RenderMetricsApp
    result={parse(matrixMultiSeries)}
    colorMode={colorMode}
    expr={'histogram_quantile(0.95, sum by (le) (rate(grafana_http_request_duration_seconds_bucket{job="hosted-grafana"}[5m])))'}
    onRefresh={() => {}}
  />
);

export const ControlsPreview = ({ colorMode }: P) => (
  <RenderMetricsApp
    result={parse(matrixMultiSeries)}
    colorMode={colorMode}
    expr="rate(node_cpu_seconds_total[5m])"
    onRefresh={() => {}}
    onSelectRange={() => {}}
  />
);
