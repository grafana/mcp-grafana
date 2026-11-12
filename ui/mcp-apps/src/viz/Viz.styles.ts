import { css } from '@emotion/css';

/**
 * Styles for the visualization components.
 *
 * Follows the package convention of a styles factory alongside the component
 * (see `McpAppShell.styles.ts`, `TraceViewer.styles.ts`). Values that vary per
 * render — a chart's height, a stat's threshold colour — are passed as CSS
 * custom properties rather than inline styles, the same way `TraceViewer` sizes
 * its span bars with `--trace-width`.
 */
export const getVizStyles = () => ({
  /** Chart host. Height comes from `--viz-height`. */
  chart: css({
    width: '100%',
    height: 'var(--viz-height, 240px)',
  }),



  /** Responsive grid of stats — the N-series instant case. */
  statGroup: css({
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))',
    gap: 16,
    alignItems: 'start',
  }),

  /** A single stat. Accent colour comes from `--viz-accent`. */
  stat: css({
    display: 'flex',
    flexDirection: 'column',
    gap: 2,
    minWidth: 0,
    alignItems: 'flex-start',
    '&[data-align="center"]': { alignItems: 'center' },
  }),

  statLabel: css({
    fontSize: 12,
    color: 'var(--mcp-muted-foreground)',
    maxWidth: '100%',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }),

  statValue: css({
    fontSize: 32,
    fontWeight: 600,
    lineHeight: 1.1,
    fontVariantNumeric: 'tabular-nums',
    color: 'var(--viz-value-color, var(--mcp-foreground))',
  }),

  statSuffix: css({
    fontSize: 18,
    fontWeight: 500,
    color: 'var(--mcp-muted-foreground)',
  }),

  bulletGroup: css({
    display: 'flex',
    flexDirection: 'column',
    gap: 14,
  }),

  bullet: css({
    display: 'flex',
    flexDirection: 'column',
    gap: 4,
    minWidth: 0,
  }),

  bulletHeader: css({
    display: 'flex',
    alignItems: 'baseline',
    justifyContent: 'space-between',
    gap: 12,
  }),

  bulletLabel: css({
    fontSize: 12,
    color: 'var(--mcp-muted-foreground)',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }),

  bulletValue: css({
    fontSize: 16,
    fontWeight: 600,
    fontVariantNumeric: 'tabular-nums',
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    color: 'var(--mcp-foreground)',
    flexShrink: 0,
  }),

  /** The scale. Bands sit behind the measure; both are positioned children. */
  bulletTrack: css({
    position: 'relative',
    height: 14,
    borderRadius: 2,
    background: 'var(--mcp-muted)',
    overflow: 'hidden',
  }),

  bulletBand: css({
    position: 'absolute',
    top: 0,
    bottom: 0,
  }),

  /** The measure: a centred bar, inset so the bands stay readable around it. */
  bulletMeasure: css({
    position: 'absolute',
    top: 4,
    bottom: 4,
    left: 0,
    width: 'var(--viz-measure, 0%)',
    background: 'var(--viz-measure-color, var(--mcp-foreground))',
    borderRadius: 2,
  }),

  /** Comparative marker, drawn across the full height like Few's bullet. */
  bulletTarget: css({
    position: 'absolute',
    top: 0,
    bottom: 0,
    left: 'var(--viz-target, 0%)',
    width: 2,
    marginLeft: -1,
    background: 'var(--mcp-foreground)',
  }),

  bulletScale: css({
    display: 'flex',
    justifyContent: 'space-between',
    fontSize: 10,
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    color: 'var(--mcp-muted-foreground)',
  }),

  tableScroll: css({
    // Wide results scroll inside the view rather than stretching the panel.
    overflowX: 'auto',
    maxHeight: 320,
    overflowY: 'auto',
  }),

  table: css({
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: 12,
    'th, td': {
      padding: '4px 10px',
      textAlign: 'left',
      borderBottom: '1px solid var(--mcp-border)',
      whiteSpace: 'nowrap',
    },
    'thead th': {
      position: 'sticky',
      top: 0,
      background: 'var(--mcp-background)',
      fontWeight: 500,
      color: 'var(--mcp-muted-foreground)',
    },
    'tbody tr:last-child th, tbody tr:last-child td': { borderBottom: 0 },
  }),

  tableNumeric: css({
    textAlign: 'right !important' as 'right',
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    fontVariantNumeric: 'tabular-nums',
  }),

  tableTime: css({
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    fontWeight: 400,
    color: 'var(--mcp-muted-foreground)',
  }),

  tableLabel: css({
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    fontWeight: 400,
  }),

  tableFooter: css({
    fontSize: 11,
    color: 'var(--mcp-muted-foreground)',
    padding: '6px 10px',
  }),

  tableEmpty: css({
    padding: '24px 0',
    textAlign: 'center',
    fontSize: 13,
    color: 'var(--mcp-muted-foreground)',
  }),

  sparkline: css({
    width: '100%',
    height: 28,
    display: 'block',
    stroke: 'var(--viz-accent)',
  }),
});
