import { Picker } from '../src/Picker';

// Labels for the chart IDs of registry/chart-types.json, which has no display names.
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
};

/** The name of a chart for people. A chart of a later contract shows as its registry name. */
export function chartLabel(chart: string): string {
  return LABELS[chart] ?? chart;
}

/**
 * Picks the chart of the viz view: the chart of the result or one of its alternatives (spec.2). The Source lists
 * only the charts that fit the data (tool.23).
 */
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
