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

  picker: css({ position: 'relative' }),

  pickerMenu: css({
    position: 'absolute',
    top: 'calc(100% + 4px)',
    right: 0,
    zIndex: 2,
    minWidth: 160,
    // A chat panel can be very narrow; never overflow it.
    maxWidth: 'calc(100vw - 12px)',
    margin: 0,
    padding: 4,
    listStyle: 'none',
    borderRadius: 6,
    border: '1px solid var(--mcp-border)',
    background: 'var(--mcp-background)',
    boxShadow: '0 4px 16px rgb(0 0 0 / 12%)',
  }),

  /** A ghost Button, stretched to a full-width menu row. */
  pickerOption: css({
    width: '100%',
    justifyContent: 'flex-start',
    '&[aria-selected="true"]': { background: 'var(--mcp-accent)' },
  }),

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
