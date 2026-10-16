import { css } from '@emotion/css';
import { getDesignTokens } from '../design';

/** Styles for `RenderMetricsApp`, per the package's styles-factory convention. */
export const getRenderMetricsAppStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();

  return {
    /**
     * Toolbar plus chart, one block like the trace viewer's controls and
     * waterfall. The extra top margin adds to the shell's content gap, so the
     * toolbar has as much room above it as the chart leaves below it.
     */
    viz: css({
      display: 'grid',
      gap: spacing[3],
      minWidth: 0,
      marginBlockStart: spacing[1],
    }),

    /** The chart's control row: visualization picker and icon controls. */
    controls: css({
      display: 'flex',
      gap: spacing[2],
      justifyContent: 'flex-end',
      alignItems: 'center',
      flexWrap: 'wrap',
    }),

    picker: css({ position: 'relative' }),

    pickerMenu: css({
      position: 'absolute',
      top: `calc(100% + ${spacing[1]})`,
      right: 0,
      zIndex: 2,
      minWidth: 160,
      // A chat panel can be very narrow; never overflow it.
      maxWidth: `calc(100vw - ${spacing[3]})`,
      margin: 0,
      padding: spacing[1],
      listStyle: 'none',
      borderRadius: borderRadius.md,
      border: `${borderWidth.thin} solid ${colors.line.border}`,
      background: colors.surface.background,
      boxShadow: '0 4px 16px rgb(0 0 0 / 12%)',
    }),

    /** A ghost Button, stretched to a full-width menu row. */
    pickerOption: css({
      width: '100%',
      justifyContent: 'flex-start',
      '&[aria-selected="true"]': { background: colors.interactive.accent },
    }),

    /** Square, icon-only variant of the shell's xs Button (24px tall). */
    iconButton: css({ width: spacing[6], padding: 0 }),

    /** Refresh icon while a refresh is in flight. */
    spin: css({
      animation: 'mcp-spin 800ms linear infinite',
      '@media (prefers-reduced-motion: reduce)': { animation: 'none' },
    }),

    /**
     * Tooltip for an icon-only control, shown on hover and keyboard focus. Text
     * comes from `data-tooltip`; the button carries its own accessible name, so
     * this is visual only. Anchored to the right edge, since the controls sit
     * at the end of the row.
     */
    tooltip: css({
      position: 'relative',
      display: 'inline-flex',
      '&::after': {
        content: 'attr(data-tooltip)',
        position: 'absolute',
        top: `calc(100% + ${spacing[1.5]})`,
        right: 0,
        zIndex: 3,
        padding: `${spacing[1]} ${spacing[2]}`,
        borderRadius: borderRadius.sm,
        fontSize: typography.fontSize.ui.xs,
        lineHeight: 1.4,
        whiteSpace: 'nowrap',
        pointerEvents: 'none',
        color: colors.surface.background,
        background: colors.surface.foreground,
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
      top: spacing[1],
      left: '50%',
      transform: 'translateX(-50%)',
      zIndex: 1,
      maxWidth: `calc(100% - ${spacing[4]})`,
      padding: `${spacing[1]} ${spacing[2.5]}`,
      borderRadius: borderRadius.md,
      fontSize: typography.fontSize.ui.xs,
      lineHeight: 1.4,
      textAlign: 'center',
      // Must not block the drag it describes.
      pointerEvents: 'none',
      color: colors.surface.foreground,
      background: colors.interactive.muted,
      border: `${borderWidth.thin} solid ${colors.line.border}`,
    }),

    /** The PromQL expression reads as code, not prose. */
    query: css({
      fontFamily: typography.fontFamily.monospace,
      fontSize: typography.fontSize.ui.md,
      overflowWrap: 'anywhere',
    }),
    empty: css({
      padding: `${spacing[7]} 0`,
      textAlign: 'center',
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.sm,
    }),
    // Direct children of the shell's content, which already spaces them.
    warning: css({ fontSize: typography.fontSize.ui.sm, color: colors.interactive.mutedForeground }),
    debug: css({
      fontSize: typography.fontSize.ui.xs,
      color: colors.interactive.mutedForeground,
      fontFamily: typography.fontFamily.monospace,
    }),
  };
};
