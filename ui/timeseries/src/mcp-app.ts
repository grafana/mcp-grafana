import { App } from "@modelcontextprotocol/ext-apps";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";

// Grafana-inspired color palette
const SERIES_COLORS = [
  "#7EB26D", "#EAB839", "#6ED0E0", "#EF843C", "#E24D42",
  "#1F78C1", "#BA43A9", "#705DA0", "#508642", "#CCA300",
  "#447EBC", "#C15C17", "#890F02", "#0A437C", "#6D1F62",
];

// --- Prometheus data parsing ---

interface PromSample {
  metric: Record<string, string>;
  values?: [number, string][];
  value?: [number, string];
}

interface PromResult {
  data?: PromSample[];
  Data?: PromSample[];
}

function parsePromResult(raw: unknown): { labels: string[]; data: uPlot.AlignedData } | null {
  if (!raw) return null;

  let samples: PromSample[];

  if (Array.isArray(raw)) {
    samples = raw;
  } else {
    const obj = raw as PromResult;
    samples = obj.data ?? obj.Data ?? [];
  }

  if (!Array.isArray(samples) || samples.length === 0) return null;

  // Range query (matrix) — each sample has `values`
  if (samples[0].values) {
    return parseMatrix(samples);
  }

  // Instant query (vector) — each sample has `value`
  if (samples[0].value) {
    return parseVector(samples);
  }

  return null;
}

function seriesLabel(metric: Record<string, string>): string {
  const name = metric.__name__ || "";
  const rest = Object.entries(metric)
    .filter(([k]) => k !== "__name__")
    .map(([k, v]) => `${k}="${v}"`)
    .join(", ");
  if (name && rest) return `${name}{${rest}}`;
  if (name) return name;
  if (rest) return `{${rest}}`;
  return "value";
}

function parseMatrix(samples: PromSample[]): { labels: string[]; data: uPlot.AlignedData } {
  // Collect all unique timestamps across all series
  const tsSet = new Set<number>();
  for (const s of samples) {
    for (const [ts] of s.values!) {
      tsSet.add(ts);
    }
  }
  const timestamps = Array.from(tsSet).sort((a, b) => a - b);
  const tsIndex = new Map(timestamps.map((ts, i) => [ts, i]));

  // Build uPlot data: [timestamps, series1, series2, ...]
  const data: (number | null)[][] = [timestamps];
  const labels: string[] = [];

  for (const s of samples) {
    labels.push(seriesLabel(s.metric));
    const vals: (number | null)[] = new Array(timestamps.length).fill(null);
    for (const [ts, v] of s.values!) {
      const idx = tsIndex.get(ts);
      if (idx !== undefined) {
        const num = parseFloat(v);
        vals[idx] = isNaN(num) ? null : num;
      }
    }
    data.push(vals);
  }

  return { labels, data: data as uPlot.AlignedData };
}

function parseVector(samples: PromSample[]): { labels: string[]; data: uPlot.AlignedData } {
  const labels: string[] = [];
  const timestamps: number[] = [];
  const values: (number | null)[] = [];

  for (const s of samples) {
    labels.push(seriesLabel(s.metric));
    const [ts, v] = s.value!;
    timestamps.push(ts);
    const num = parseFloat(v);
    values.push(isNaN(num) ? null : num);
  }

  return { labels, data: [timestamps, values] as uPlot.AlignedData };
}

// --- Theme detection ---

function isDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function themeColors() {
  const dark = isDark();
  return {
    bg: dark ? "#111217" : "#ffffff",
    text: dark ? "#d5d6db" : "#1d1f22",
    grid: dark ? "#2c2f3e" : "#e4e7e7",
    axis: dark ? "#8e8fa1" : "#585959",
  };
}

// --- Chart rendering ---

let chart: uPlot | null = null;
let currentData: uPlot.AlignedData | null = null;
let currentLabels: string[] = [];
let originalXRange: [number, number] | null = null;

const chartContainer = document.getElementById("chart-container")!;
const statusEl = document.getElementById("status")!;
const errorEl = document.getElementById("error")!;
const titleEl = document.getElementById("title")!;
const metaEl = document.getElementById("meta")!;
const legendEl = document.getElementById("legend")!;

function buildOpts(labels: string[], width: number): uPlot.Options {
  const tc = themeColors();

  const series: uPlot.Series[] = [
    { label: "Time" },
    ...labels.map((label, i) => ({
      label,
      stroke: SERIES_COLORS[i % SERIES_COLORS.length],
      width: 1.5,
      points: { show: false },
    })),
  ];

  return {
    width,
    height: 250,
    legend: { show: false },
    cursor: {
      drag: { x: true, y: false, setScale: true },
    },
    scales: {
      x: { time: true },
    },
    axes: [
      {
        stroke: tc.axis,
        grid: { stroke: tc.grid, width: 1 },
        ticks: { stroke: tc.grid, width: 1 },
        font: "11px Inter, sans-serif",
      },
      {
        stroke: tc.axis,
        grid: { stroke: tc.grid, width: 1 },
        ticks: { stroke: tc.grid, width: 1 },
        font: "11px Inter, sans-serif",
        size: 60,
      },
    ],
    series,
  };
}

function renderChart(labels: string[], data: uPlot.AlignedData) {
  currentData = data;
  currentLabels = labels;

  if (data[0].length > 1) {
    originalXRange = [data[0][0] as number, data[0][data[0].length - 1] as number];
  }

  statusEl.style.display = "none";
  errorEl.style.display = "none";

  if (chart) {
    chart.destroy();
    chart = null;
  }

  const width = chartContainer.clientWidth || 600;
  const opts = buildOpts(labels, width);
  chart = new uPlot(opts, data, chartContainer);

  renderLegend(labels);
}

function renderLegend(labels: string[]) {
  legendEl.innerHTML = "";
  labels.forEach((label, i) => {
    const item = document.createElement("div");
    item.className = "legend-item";

    const swatch = document.createElement("span");
    swatch.className = "legend-swatch";
    swatch.style.background = SERIES_COLORS[i % SERIES_COLORS.length];

    const text = document.createElement("span");
    text.textContent = label;

    item.appendChild(swatch);
    item.appendChild(text);

    item.addEventListener("click", () => {
      if (!chart) return;
      const seriesIdx = i + 1;
      const isVisible = chart.series[seriesIdx].show !== false;
      chart.setSeries(seriesIdx, { show: !isVisible });
      item.style.opacity = isVisible ? "0.4" : "1";
    });

    legendEl.appendChild(item);
  });
}

function showError(msg: string) {
  errorEl.textContent = msg;
  errorEl.style.display = "block";
}

// --- Resize handling ---

const resizeObserver = new ResizeObserver(() => {
  if (chart && currentData) {
    const width = chartContainer.clientWidth || 600;
    chart.setSize({ width, height: 250 });
  }
});
resizeObserver.observe(chartContainer);

// --- Dev mode: mock data for local testing ---

function isDevMode(): boolean {
  return window.parent === window;
}

function generateMockData(): PromResult {
  const now = Math.floor(Date.now() / 1000);
  const step = 15;
  const points = 120;
  const series = [
    { __name__: "http_requests_total", method: "GET", status: "200" },
    { __name__: "http_requests_total", method: "GET", status: "500" },
    { __name__: "http_requests_total", method: "POST", status: "200" },
  ];

  return {
    data: series.map((metric, si) => {
      const base = [100, 5, 40][si];
      const values: [number, string][] = [];
      let v = base;
      for (let i = 0; i < points; i++) {
        v += (Math.random() - 0.48) * base * 0.1;
        v = Math.max(0, v);
        values.push([now - (points - i) * step, v.toFixed(2)]);
      }
      return { metric, values };
    }),
  };
}

// --- Handle incoming tool results ---

function handleToolResult(result: { content?: { type: string; text?: string }[]; structuredContent?: unknown }) {
  try {
    let raw: unknown;

    if (result.structuredContent) {
      raw = result.structuredContent;
    } else {
      const textContent = result.content?.find((c) => c.type === "text");
      if (textContent?.text) {
        raw = JSON.parse(textContent.text);
      }
    }

    if (!raw) {
      statusEl.textContent = "No data returned";
      statusEl.style.display = "block";
      return;
    }

    // The Prometheus tool wraps results in {data: ..., hints: ...}
    const promResult = (raw as Record<string, unknown>).data ?? (raw as Record<string, unknown>).Data ?? raw;

    const parsed = parsePromResult(promResult);
    if (!parsed || parsed.data[0].length === 0) {
      statusEl.textContent = "Query returned no data points";
      statusEl.style.display = "block";
      return;
    }

    const nPoints = parsed.data[0].length;
    const nSeries = parsed.labels.length;
    metaEl.textContent = `${nSeries} series · ${nPoints} points`;

    renderChart(parsed.labels, parsed.data);
  } catch (e) {
    showError(`Failed to render: ${(e as Error).message}`);
  }
}

// --- Reset zoom ---

document.getElementById("btn-reset-zoom")!.addEventListener("click", () => {
  if (chart && originalXRange) {
    chart.setScale("x", { min: originalXRange[0], max: originalXRange[1] });
  }
});

// Theme change
window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  if (currentData && currentLabels.length > 0) {
    renderChart(currentLabels, currentData);
  }
});

// --- Bootstrap: dev mode or MCP App mode ---

if (isDevMode()) {
  // Running standalone in a browser — use mock data
  titleEl.textContent = "http_requests_total (mock)";

  const refresh = () => {
    handleToolResult({ structuredContent: generateMockData() });
  };

  refresh();
  document.getElementById("btn-refresh")!.addEventListener("click", refresh);
} else {
  // Running inside an MCP host iframe
  const app = new App({ name: "Prometheus Time Series", version: "1.0.0" });

  let lastToolInput: { name: string; arguments: Record<string, unknown> } | null = null;

  app.ontoolinput = (params: { name: string; arguments?: Record<string, unknown> }) => {
    lastToolInput = { name: params.name, arguments: params.arguments ?? {} };
    const expr = params.arguments?.expr as string;
    if (expr) {
      titleEl.textContent = expr;
    }
  };

  app.ontoolresult = (result: Parameters<NonNullable<typeof app.ontoolresult>>[0]) => {
    handleToolResult(result);
  };

  document.getElementById("btn-refresh")!.addEventListener("click", async () => {
    if (!lastToolInput) return;
    try {
      statusEl.textContent = "Refreshing…";
      statusEl.style.display = "block";
      const result = await app.callServerTool({
        name: lastToolInput.name,
        arguments: lastToolInput.arguments,
      });
      app.ontoolresult!(result as Parameters<NonNullable<typeof app.ontoolresult>>[0]);
    } catch (e) {
      showError(`Refresh failed: ${(e as Error).message}`);
    }
  });

  app.connect();
}
