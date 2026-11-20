/**
 * The delta between the design canvas ("App shell", Freya / Visual Refresh) and the code, one entry per design
 * component. This file is the single source for the component pages and the Overview. It is written by hand from an
 * audit of the design boards against the code, so update it when a delta is closed or the design changes.
 * Token values are not listed here; the Tokens page reads them live.
 */

export type Size = 'small' | 'medium' | 'large';
/**
 * Who has to act: `repo` changes this repository, `renderer` needs a change in the external viz Renderer or the
 * server, and `decision` needs a design call before anyone can build it.
 */
export type Owner = 'repo' | 'renderer' | 'decision';

export interface Delta {
  aspect: string;
  design: string;
  code: string;
  size: Size;
  owner: Owner;
  note?: string;
}

export interface ComponentDeltas {
  id: string;
  title: string;
  /** Names of the design artboards the component is drawn on. Names only: the canvas is private. */
  artboards: string[];
  /** The code that plays this role today. */
  counterpart: string;
  summary: string;
  deltas: Delta[];
}

export const SIZE_ORDER: Size[] = ['large', 'medium', 'small'];

export const COMPONENTS: ComponentDeltas[] = [
  {
    id: 'shell',
    title: 'App shell',
    artboards: ['App shell', 'App shell - dark, light, desktop, phone', 'App shell - notes for engineers'],
    counterpart: 'src/McpAppShell.tsx, McpAppShell.styles.ts',
    summary: 'The outer frame and header shared by every response.',
    deltas: [
      { aspect: 'Card radius', design: 'lg = 12px', code: '16px, hardcoded', size: 'small', owner: 'repo' },
      { aspect: 'Card padding', design: 'Header 12px; content 16px with no top padding', code: '20px on every side (16px compact, 12px under 480px)', size: 'small', owner: 'repo' },
      { aspect: 'Header label', design: '"Grafana Cloud", 14px medium', code: 'The product prop ("Grafana"), 12px semibold, optional "· scope"', size: 'small', owner: 'repo' },
      { aspect: 'Header logo', design: 'Grafana.png at 20x20', code: 'grafana.svg at 16x16', size: 'small', owner: 'repo' },
      { aspect: 'Open in Grafana', design: 'Secondary md button with a trailing external-link icon; icon-only lg button on phone', code: 'Ghost xs link, same on every viewport', size: 'medium', owner: 'repo' },
      { aspect: 'Header alignment', design: 'Row, centered, space-between', code: 'flex-start, wraps (flex 1 1 240px)', size: 'small', owner: 'repo' },
      { aspect: 'Card border', design: 'weak border token (Overview card uses medium)', code: '--mcp-border, much stronger in dark', size: 'small', owner: 'repo', note: 'Follows the token comparison.' },
    ],
  },
  {
    id: 'summary',
    title: 'Response summary',
    artboards: ['Response summary - error', 'Response summary - neutral, success, warning, error', 'Response summary - alternatives', 'Response summary - notes for engineers'],
    counterpart: 'The summary strip inside McpAppShell (label, title, description)',
    summary: 'A short verdict that leads the response: a headline, one supporting line and a status.',
    deltas: [
      { aspect: 'Optional block', design: 'Optional; renders first, above Query and the surface', code: 'Mandatory strip in the shell; `label` is required', size: 'medium', owner: 'repo' },
      { aspect: 'Four statuses', design: 'neutral, success, warning, error', code: 'None. `status` is a free ReactNode slot used as text.', size: 'large', owner: 'repo', note: 'A new status model, and the code has no warning tone.' },
      { aspect: 'Status icons', design: 'check-circle, exclamation-triangle, exclamation-circle; none for neutral', code: 'No icons', size: 'large', owner: 'repo' },
      { aspect: 'Three looks', design: 'text (default), icon, tinted', code: 'One look', size: 'large', owner: 'decision', note: 'The design offers alternatives; pick one before building.' },
      { aspect: 'ARIA roles', design: 'neutral region; others status (polite live region)', code: 'article labelled by the title; no role on the summary', size: 'medium', owner: 'repo' },
      { aspect: 'Headline', design: 'h2 styled as Freya h5: 400, 14px/22px, status text color', code: 'h1, 12px/18px, weight 400, always foreground', size: 'medium', owner: 'decision', note: 'Depends on the type-scale decision (14px vs 12px).' },
      { aspect: 'Body text', design: 'p in secondary text, 14px', code: 'description, 12px muted foreground', size: 'small', owner: 'decision' },
      { aspect: 'Surface', design: 'Padding 16px, background secondary, 1px weak border, radius 8px', code: 'Padding 16px 20px, solid surface, no border, radius 6px', size: 'small', owner: 'repo' },
      { aspect: 'Orange label', design: 'No counterpart', code: 'Orange 12px caption ("Data source query", "Trace")', size: 'small', owner: 'decision', note: 'Keep, move or remove: the design does not show it.' },
    ],
  },
  {
    id: 'blocks',
    title: 'Content blocks',
    artboards: ['Content blocks', 'Content blocks - dark, light, no surface, no summary, phone', 'Content blocks - notes for engineers'],
    counterpart: 'McpAppShell content stack, src/McpAppSection.tsx',
    summary: 'The fixed order and spacing of the blocks inside the shell.',
    deltas: [
      { aspect: 'Block gap', design: '12px (Stack gap 1.5)', code: '12px (spacing[3])', size: 'small', owner: 'repo', note: 'Already matches.' },
      { aspect: 'Block order', design: 'Summary (optional), Query (required), interactive surface (optional)', code: 'Summary, then children. No Query slot.', size: 'medium', owner: 'repo' },
      { aspect: 'Surface card', design: 'Secondary background, 1px weak border, radius 8px, padding 16px', code: 'McpAppSection: padding 16px 20px, radius 6px, no border', size: 'medium', owner: 'repo' },
      { aspect: 'Surface title', design: 'h5 title with a secondary body line', code: 'Orange 12px medium section title', size: 'small', owner: 'decision' },
      { aspect: 'Alerts placement', design: 'Above or below the blocks', code: 'Footer only', size: 'small', owner: 'repo' },
    ],
  },
  {
    id: 'alerts',
    title: 'Alerts',
    artboards: ['Alerts - condensed, with action and close', 'Alerts - Freya default (reference)', 'Alerts - dark, light, phone', 'Alerts - content and controls', 'Alerts - notes for engineers'],
    counterpart: "McpAppShell `feedback` and `tip`; the ad hoc .warning in RenderMetricsApp",
    summary: 'Inline status messages about the request itself, in four severities.',
    deltas: [
      { aspect: 'Severities', design: 'success, warning, error, info', code: 'success, error, info. The warning notice is a separate ad hoc style.', size: 'medium', owner: 'repo' },
      { aspect: 'Surface', design: 'subtle background + 1px subtle border, radius 8px, padding 8px', code: 'color-mix 15% tint, no border, radius 6px, padding 8px 12px', size: 'medium', owner: 'repo', note: 'Needs the subtle tokens (missing in code).' },
      { aspect: 'Text', design: 'Title in primary text, description in secondary, 12px', code: 'Whole message in the status text color, 12px', size: 'medium', owner: 'repo' },
      { aspect: 'Title + description', design: 'Two-line or single sentence', code: 'One message node', size: 'medium', owner: 'repo' },
      { aspect: 'Icons', design: 'check, exclamation-triangle, exclamation-triangle, info-circle', code: 'lucide CircleCheck, CircleAlert, Info at 16px', size: 'small', owner: 'repo' },
      { aspect: 'Action', design: 'Primary sm button', code: 'Ghost xs button', size: 'medium', owner: 'repo' },
      { aspect: 'Close button', design: 'Icon button with aria-label "Close" (unwired in the prototype)', code: 'None', size: 'small', owner: 'repo' },
      { aspect: 'Phone layout', design: 'Under ~480px: close top right, action below the text', code: 'Under 360px: wraps, action full width', size: 'small', owner: 'repo' },
      { aspect: 'ARIA roles', design: 'error alert; others status', code: 'error alert; others status', size: 'small', owner: 'repo', note: 'Already matches.' },
    ],
  },
  {
    id: 'buttons',
    title: 'Action buttons',
    artboards: ['Action buttons', 'Action buttons - dark, light, primary only, phone', 'Action buttons - notes for engineers'],
    counterpart: 'McpAppShell `primaryAction` / `secondaryAction` (no call site uses them today)',
    summary: 'The row of follow-up actions at the bottom of a response.',
    deltas: [
      { aspect: 'Primary fill', design: 'Neutral and inverted (near-white in dark, near-black in light)', code: 'Blue --mcp-primary', size: 'medium', owner: 'decision', note: 'Needs a design decision, not just a restyle.' },
      { aspect: 'Secondary', design: 'Freya secondary', code: 'muted background, 1px border', size: 'small', owner: 'repo' },
      { aspect: 'Sizes', design: 'md on desktop, lg on phone', code: 'xs 24 / sm 30 / default 36px; no lg', size: 'small', owner: 'repo' },
      { aspect: 'Radius', design: 'md = 8px', code: '6px, hardcoded', size: 'small', owner: 'repo' },
      { aspect: 'Phone layout', design: 'Row stays, lg size', code: 'At 360px or less: stacked, stretched', size: 'small', owner: 'repo' },
    ],
  },
  {
    id: 'query',
    title: 'Query',
    artboards: ['Query - collapsed and expanded', 'Query - dark, light, chart types, phone', 'Query - notes for engineers'],
    counterpart: 'None. The query shows as the summary title (describeQueries in VizApplication, expr in RenderMetricsApp).',
    summary: 'Shows the query behind the response so the data is traceable. Required in the design.',
    deltas: [
      { aspect: 'The block itself', design: 'Always shown between the summary and the surface; 16px padding, weak border, radius 8px', code: 'No component', size: 'large', owner: 'repo' },
      { aspect: 'Collapsed state', design: 'Chevron, datasource chip, one truncated monospace line', code: 'Query wraps in the summary title', size: 'large', owner: 'repo' },
      { aspect: 'Expanded state', design: 'Full query in a code panel with syntax highlighting, plus a meta line (range, step, series, run time)', code: 'None', size: 'large', owner: 'repo', note: 'Series count and run time are mock values in the design; check what the server returns.' },
      { aspect: 'Datasource chip', design: 'Bordered chip with a database icon and the name', code: 'Only the uid in the description', size: 'medium', owner: 'repo' },
      { aspect: 'Copy', design: 'Copies the query; icon turns into a check for 1.5s', code: 'None (Trace has a link-copy button)', size: 'medium', owner: 'repo' },
      { aspect: 'Add to chat', design: 'Short text line via sendMessage; the query goes as updateModelContext', code: 'None (only askAboutRange for a brushed window)', size: 'large', owner: 'decision', note: 'Host-dependent: needs sendMessage support and the permission prompt behaviour.' },
      { aspect: 'Syntax highlighting', design: 'Functions, numbers/variables, strings, punctuation, each with a status token', code: 'None; no warning token either', size: 'medium', owner: 'repo' },
      { aspect: 'Multiple queries', design: 'Not designed (one query per chart)', code: 'query_datasource supports several ("+N more")', size: 'small', owner: 'decision' },
    ],
  },
  {
    id: 'controls',
    title: 'Visualization controls',
    artboards: ['Visualization controls - time range', 'Visualization controls - dark, light, phone', 'Visualization controls - notes for engineers'],
    counterpart: 'The controls row in src/metrics/RenderMetricsApp.tsx, src/Picker.tsx, app/ChartPicker.tsx',
    summary: 'The toolbar at the top of the surface; for now the time range.',
    deltas: [
      { aspect: 'Time range toolbar', design: 'Button group: back, clock + range label, forward, zoom out', code: 'No time range control for query_prometheus; a brush hands a window to the agent', size: 'large', owner: 'decision', note: 'Needs a defined behaviour: re-query, or a host call.' },
      { aspect: 'Preset menu', design: 'Three columns (Minutes, Hours, Days), selected preset is the primary button', code: 'Single-column Picker', size: 'medium', owner: 'repo' },
      { aspect: 'Container', design: 'Bordered box, secondary background, radius 8px, 16px padding', code: 'A bare right-aligned row, 8px gap', size: 'small', owner: 'repo' },
      { aspect: 'Button style', design: 'Joined ToolbarButton group', code: 'Separate xs buttons, 24px icon buttons', size: 'small', owner: 'repo' },
      { aspect: 'Extra controls', design: 'No refresh, no chart picker, no brush', code: 'Refresh, chart-type picker and a brush toggle', size: 'small', owner: 'decision', note: 'Keep them? The design omits them.' },
      { aspect: 'query_datasource time picker', design: 'The toolbar above', code: "Drawn by the Renderer (@grafana/ui); the App cannot replace or hide it", size: 'large', owner: 'renderer' },
    ],
  },
];

/** Cross-cutting calls that no single component can settle. */
export const OPEN_DECISIONS: { title: string; design: string; code: string }[] = [
  { title: 'Primary button color', design: 'Neutral and inverted', code: 'Blue' },
  { title: 'Type scale', design: '14px headings and body, with line-heights', code: '12px for all UI text, no line-heights' },
  { title: 'Spacing unit', design: '8px (spacing(2) = 16px)', code: '4px (spacing[2] = 8px); aliasing the keys would double every gap' },
  { title: 'Summary label', design: 'No orange caption above the headline', code: 'An orange label such as "Data source query"' },
  { title: 'Controls the design omits', design: 'No refresh, chart picker or brush', code: 'All three exist and are used' },
  { title: 'Summary look', design: 'Three alternatives: text, icon, tinted', code: 'A single plain look' },
];
