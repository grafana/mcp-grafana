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

  /** Square, icon-only variant of the shell's xs Button. */
  iconButton: css({ width: 24, padding: 0 }),

  /**
   * Tooltip for an icon-only control, shown on hover and keyboard focus. Text
   * comes from `data-tooltip`; the button carries its own accessible name, so
   * this is visual only. Anchored to the right edge, since the control sits
   * at the end of the row.
   */
  tooltip: css({
    position: 'relative',
    display: 'inline-flex',
    '&::after': {
      content: 'attr(data-tooltip)',
      position: 'absolute',
      top: 'calc(100% + 6px)',
      right: 0,
      zIndex: 3,
      padding: '4px 8px',
      borderRadius: 4,
      fontSize: 11,
      lineHeight: 1.4,
      whiteSpace: 'nowrap',
      pointerEvents: 'none',
      color: 'var(--mcp-background)',
      background: 'var(--mcp-foreground)',
      opacity: 0,
      transition: 'opacity 120ms ease-in',
    },
    '&:hover::after, &:has(:focus-visible)::after': { opacity: 1, transitionDelay: '300ms' },
    '@media (prefers-reduced-motion: reduce)': { '&::after': { transition: 'none' } },
  }),

  /** Positioning context for the selection hint. */
  vizArea: css({ position: 'relative' }),

  /** Instruction shown over the top of the chart while selection is armed. */
  selectHint: css({
    position: 'absolute',
    top: 4,
    left: '50%',
    transform: 'translateX(-50%)',
    zIndex: 1,
    maxWidth: 'calc(100% - 16px)',
    padding: '3px 10px',
    borderRadius: 999,
    fontSize: 11,
    lineHeight: 1.4,
    textAlign: 'center',
    // Must not block the drag it describes.
    pointerEvents: 'none',
    color: 'var(--mcp-foreground)',
    background: 'var(--mcp-muted)',
    border: '1px solid var(--mcp-border)',
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
