import { Picker } from '../src/Picker';

// registry/chart-types.json has no display names.
const LABELS: Record<string, string> = {
  timeseries: 'Time series',
  barchart: 'Bar chart',
  histogram: 'Histogram',
  candlestick: 'Candlestick',
  boxplot: 'Box plot',
  piechart: 'Pie chart',
  funnel: 'Funnel',
  heatmap: 'Heatmap',
  geomap: 'Geomap',
  'state-timeline': 'State timeline',
  'status-history': 'Status history',
  nodeGraph: 'Node graph',
  sankey: 'Sankey',
  chord: 'Chord',
  treemap: 'Treemap',
  sunburst: 'Sunburst',
  radar: 'Radar',
  parallel: 'Parallel coordinates',
  streamgraph: 'Streamgraph',
  bubble: 'Bubble',
  gauge: 'Gauge',
  stat: 'Stat',
};

/** The display name of a chart; a chart this list lacks shows its ID. */
export function chartLabel(chart: string): string {
  return LABELS[chart] ?? chart;
}

/** Picks the result's chart or one of its alternatives (spec.2, tool.23). */
export function ChartPicker({
  options,
  value,
  onChange,
}: {
  options: string[];
  value: string;
  onChange: (chart: string) => void;
}) {
  return (
    <Picker
      label="Chart"
      triggerLabel={`Chart: ${chartLabel(value)}`}
      options={options.map((chart) => ({ value: chart, label: chartLabel(chart) }))}
      value={value}
      onChange={onChange}
    />
  );
}
