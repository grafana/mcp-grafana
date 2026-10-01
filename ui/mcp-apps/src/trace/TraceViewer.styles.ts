import { css } from '@emotion/css';
import { getDesignTokens } from '../design';

export const getTraceViewerStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();

  return {
    viewer: css({
      display: 'grid',
      gridTemplateColumns: 'minmax(0, 1.4fr) minmax(280px, 1fr)',
      gap: spacing[5],
      minWidth: 0,
      alignItems: 'start',
    }),
    tracePane: css({
      minWidth: 0,
    }),
    detailPane: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[3],
      minWidth: 0,
      paddingInlineStart: spacing[5],
      borderInlineStart: `${borderWidth.thin} solid ${colors.line.border}`,
    }),
    paneHeading: css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: spacing[2],
      minWidth: 0,
      marginBlockEnd: spacing[2],
    }),
    heading: css({
      minWidth: 0,
      margin: 0,
      color: colors.surface.foreground,
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.semibold,
      lineHeight: '18px',
      overflowWrap: 'anywhere',
    }),
    controls: css({
      display: 'grid',
      gridTemplateColumns: 'minmax(150px, 1fr) auto auto',
      gap: spacing[2],
      minWidth: 0,
      marginBlockEnd: spacing[2],
    }),
    search: css({
      boxSizing: 'border-box',
      width: '100%',
      minWidth: 0,
      minHeight: spacing[9],
      padding: `${spacing[1.5]} ${spacing[2.5]}`,
      border: `${borderWidth.thin} solid ${colors.line.input}`,
      borderRadius: borderRadius.md,
      outline: 0,
      backgroundColor: colors.surface.background,
      color: colors.surface.foreground,
      fontFamily: typography.fontFamily.ui,
      fontSize: typography.fontSize.ui.sm,
      lineHeight: '18px',
      '&::placeholder': {
        color: colors.interactive.mutedForeground,
      },
      '&:focus-visible': {
        borderColor: colors.line.ring,
        boxShadow: `0 0 0 ${borderWidth.medium} ${colors.line.ring}`,
      },
    }),
    count: css({
      minHeight: '18px',
      margin: `0 0 ${spacing[2]} 0`,
      color: colors.interactive.mutedForeground,
      fontVariantNumeric: 'tabular-nums',
    }),
    viewport: css({
      position: 'relative',
      height: '336px',
      minWidth: 0,
      overflow: 'auto',
      overscrollBehavior: 'contain',
      border: `${borderWidth.thin} solid ${colors.line.border}`,
      borderRadius: borderRadius.md,
      backgroundColor: colors.surface.background,
    }),
    waterfall: css({
      minWidth: 0,
    }),
    axis: css({
      position: 'sticky',
      zIndex: 2,
      top: 0,
      display: 'grid',
      gridTemplateColumns: 'minmax(120px, 42%) minmax(0, 1fr) 60px',
      gap: spacing[2],
      alignItems: 'center',
      height: '38px',
      paddingInline: spacing[3],
      borderBlockEnd: `${borderWidth.thin} solid ${colors.line.border}`,
      backgroundColor: colors.surface.background,
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.xs,
      lineHeight: '16px',
    }),
    ticks: css({
      display: 'flex',
      justifyContent: 'space-between',
      minWidth: 0,
      fontVariantNumeric: 'tabular-nums',
      '@container (max-width: 480px)': {
        '& > span:nth-child(2), & > span:nth-child(4)': {
          display: 'none',
        },
      },
    }),
    rows: css({
      position: 'relative',
      minWidth: 0,
    }),
    row: css({
      position: 'absolute',
      insetInline: 0,
      display: 'grid',
      gridTemplateColumns: 'minmax(120px, 42%) minmax(0, 1fr) 60px',
      gap: spacing[2],
      alignItems: 'center',
      boxSizing: 'border-box',
      width: '100%',
      height: '48px',
      paddingInline: spacing[3],
      border: 0,
      borderBlockEnd: `${borderWidth.thin} solid ${colors.line.border}`,
      borderRadius: 0,
      background: 'transparent',
      color: colors.surface.foreground,
      font: 'inherit',
      textAlign: 'start',
      cursor: 'pointer',
      '&:hover': {
        backgroundColor: colors.interactive.muted,
      },
      '&:focus-visible': {
        zIndex: 1,
        outline: `${borderWidth.medium} solid ${colors.line.ring}`,
        outlineOffset: `calc(${borderWidth.medium} * -1)`,
      },
      '&[aria-selected="true"]': {
        backgroundColor: colors.interactive.accent,
        boxShadow: `inset ${borderWidth.thick} 0 ${colors.interactive.primary}`,
      },
    }),
    spanName: css({
      minWidth: 0,
      paddingInlineStart: `calc(var(--trace-depth, 0) * ${spacing[3]})`,
      overflow: 'hidden',
    }),
    operation: css({
      display: 'block',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    }),
    service: css({
      display: 'block',
      overflow: 'hidden',
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.xs,
      lineHeight: '16px',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    }),
    exceptionMarker: css({
      color: colors.status.destructiveSubtleForeground,
      fontWeight: typography.fontWeight.semibold,
    }),
    track: css({
      position: 'relative',
      height: spacing[7],
      overflow: 'hidden',
      backgroundImage: `repeating-linear-gradient(to right, transparent 0, transparent calc(25% - ${borderWidth.thin}), ${colors.line.border} calc(25% - ${borderWidth.thin}), ${colors.line.border} 25%)`,
    }),
    bar: css({
      position: 'absolute',
      top: spacing[2],
      left: 'var(--trace-start)',
      width: 'max(var(--trace-width), 2px)',
      height: spacing[3],
      borderRadius: borderRadius.xs,
      backgroundColor: colors.interactive.mutedForeground,
      '[data-exception="true"] &': {
        backgroundColor: colors.status.destructive,
      },
      '[aria-selected="true"] &': {
        backgroundColor: colors.interactive.primary,
      },
    }),
    duration: css({
      overflow: 'hidden',
      fontVariantNumeric: 'tabular-nums',
      textAlign: 'end',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    }),
    empty: css({
      display: 'grid',
      minHeight: spacing[20],
      placeItems: 'center',
      padding: spacing[4],
      color: colors.interactive.mutedForeground,
      textAlign: 'center',
    }),
    detailHeader: css({
      display: 'flex',
      alignItems: 'flex-start',
      justifyContent: 'space-between',
      gap: spacing[3],
      minWidth: 0,
    }),
    detailHeadingGroup: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[1],
      minWidth: 0,
    }),
    detailLabel: css({
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.xs,
      lineHeight: '16px',
    }),
    detailStatus: css({
      flex: '0 0 auto',
      color: colors.interactive.mutedForeground,
      fontVariantNumeric: 'tabular-nums',
    }),
    errorStatus: css({
      color: colors.status.destructiveSubtleForeground,
      fontWeight: typography.fontWeight.medium,
    }),
    exception: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[2],
      minWidth: 0,
      padding: spacing[3],
      borderRadius: borderRadius.md,
      backgroundColor: `color-mix(in oklab, ${colors.status.destructive} 15%, transparent)`,
      color: colors.status.destructiveSubtleForeground,
    }),
    exceptionHeading: css({
      margin: 0,
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.semibold,
      lineHeight: '18px',
      overflowWrap: 'anywhere',
    }),
    exceptionMessage: css({
      margin: 0,
      color: colors.surface.foreground,
      overflowWrap: 'anywhere',
    }),
    stackTrace: css({
      maxHeight: '180px',
      margin: 0,
      padding: spacing[2],
      overflow: 'auto',
      borderRadius: borderRadius.sm,
      backgroundColor: colors.surface.inset,
      color: colors.surface.insetForeground,
      fontFamily: typography.fontFamily.monospace,
      fontSize: typography.fontSize.monospace.sm,
      lineHeight: '18px',
      whiteSpace: 'pre',
    }),
    attributes: css({
      display: 'grid',
      gridTemplateColumns: '110px minmax(0, 1fr)',
      gap: `${spacing[1.5]} ${spacing[3]}`,
      minWidth: 0,
      margin: 0,
      fontSize: typography.fontSize.ui.sm,
    }),
    attributeName: css({
      minWidth: 0,
      color: colors.interactive.mutedForeground,
      overflowWrap: 'anywhere',
    }),
    attributePair: css({
      display: 'contents',
    }),
    attributeValue: css({
      minWidth: 0,
      margin: 0,
      fontFamily: typography.fontFamily.monospace,
      fontSize: typography.fontSize.monospace.sm,
      overflowWrap: 'anywhere',
    }),
    tabs: css({
      display: 'none',
      gridTemplateColumns: '1fr 1fr',
      gap: spacing[2],
      minWidth: 0,
    }),
    traceVisible: css({
      '@container (max-width: 760px)': {
        display: 'block',
      },
    }),
    traceHidden: css({
      '@container (max-width: 760px)': {
        display: 'none',
      },
    }),
    detailVisible: css({
      '@container (max-width: 760px)': {
        display: 'flex',
      },
    }),
    detailHidden: css({
      '@container (max-width: 760px)': {
        display: 'none',
      },
    }),
    responsiveViewer: css({
      '@container (max-width: 760px)': {
        display: 'block',
      },
    }),
    responsiveTabs: css({
      '@container (max-width: 760px)': {
        display: 'grid',
        marginBlockEnd: spacing[3],
      },
    }),
    responsiveControls: css({
      '@container (max-width: 480px)': {
        gridTemplateColumns: '1fr 1fr',
        '& > input': {
          gridColumn: '1 / -1',
        },
      },
    }),
    responsiveAxis: css({
      '@container (max-width: 480px)': {
        gridTemplateColumns: 'minmax(100px, 44%) minmax(0, 1fr) 46px',
        gap: spacing[1.5],
        paddingInline: spacing[2],
      },
    }),
    responsiveRow: css({
      '@container (max-width: 480px)': {
        gridTemplateColumns: 'minmax(100px, 44%) minmax(0, 1fr) 46px',
        gap: spacing[1.5],
        paddingInline: spacing[2],
        '& > span:first-child': {
          paddingInlineStart: `calc(var(--trace-depth, 0) * ${spacing[1.5]})`,
        },
      },
    }),
    responsiveDetails: css({
      '@container (max-width: 760px)': {
        paddingInlineStart: 0,
        borderInlineStart: 0,
      },
      '@container (max-width: 480px)': {
        '& dl': {
          gridTemplateColumns: '1fr',
        },
        '& dd': {
          marginBlockEnd: spacing[1.5],
        },
      },
    }),
  };
};
