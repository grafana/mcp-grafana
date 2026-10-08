import { css } from '@emotion/css';

/** Styles for `RenderMetricsApp`, per the package's styles-factory convention. */
export const getRenderMetricsAppStyles = () => ({
  /** The chart's control row: visualization picker and selection toggle. */
  controls: css({
    display: 'flex',
    gap: 8,
    justifyContent: 'flex-end',
    alignItems: 'center',
    flexWrap: 'wrap',
    marginBottom: 8,
  }),

  vizPicker: css({ display: 'flex', gap: 4, flexWrap: 'wrap' }),

  /** The PromQL expression reads as code, not prose. */
  query: css({
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    fontSize: 13,
    overflowWrap: 'anywhere',
  }),
  empty: css({
    padding: '32px 0',
    textAlign: 'center',
    color: 'var(--mcp-muted-foreground)',
    fontSize: 13,
  }),
  warning: css({ fontSize: 12, color: 'var(--mcp-muted-foreground)', marginTop: 8 }),
  debug: css({
    fontSize: 11,
    color: 'var(--mcp-muted-foreground)',
    fontFamily: 'ui-monospace, SFMono-Regular, monospace',
    marginTop: 8,
  }),
});
