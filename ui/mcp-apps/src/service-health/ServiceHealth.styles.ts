import { css } from '@emotion/css';
import type { PillColors } from '../design';
import { type CSSVariablesByColorMode, getDesignTokens } from '../design';

import type { ServiceHealthStatus } from './types';

export const serviceHealthCSSVariables: CSSVariablesByColorMode = (() => {
  const {
    primitives: { colors },
  } = getDesignTokens();

  return {
    light: {
      'service-health-panel-surface': colors.neutral[100],
      'service-health-status-healthy-bg': `color-mix(in srgb, ${colors.green[500]} 14%, transparent)`,
      'service-health-status-healthy-text': colors.green[700],
      'service-health-status-degraded-bg': `color-mix(in srgb, ${colors.orange[500]} 14%, transparent)`,
      'service-health-status-degraded-text': colors.orange[700],
      'service-health-status-unknown-bg': `color-mix(in srgb, ${colors.neutral[500]} 14%, transparent)`,
      'service-health-status-unknown-text': colors.neutral[700],
      'service-health-latency-line': colors.green[600],
      'service-health-error-line': colors.red[600],
      'service-health-error-fill': `color-mix(in srgb, ${colors.red[500]} 20%, transparent)`,
      'service-health-rate-line': colors.yellow[700],
      'service-health-rate-fill': `color-mix(in srgb, ${colors.teal[500]} 18%, transparent)`,
    },
    dark: {
      'service-health-panel-surface': colors.smoke[800],
      'service-health-status-healthy-bg': `color-mix(in srgb, ${colors.green[400]} 18%, transparent)`,
      'service-health-status-healthy-text': colors.green[300],
      'service-health-status-degraded-bg': `color-mix(in srgb, ${colors.orange[400]} 18%, transparent)`,
      'service-health-status-degraded-text': colors.orange[300],
      'service-health-status-unknown-bg': `color-mix(in srgb, ${colors.gray[400]} 18%, transparent)`,
      'service-health-status-unknown-text': colors.gray[200],
      'service-health-latency-line': colors.green[400],
      'service-health-error-line': colors.red[400],
      'service-health-error-fill': `color-mix(in srgb, ${colors.red[400]} 24%, transparent)`,
      'service-health-rate-line': colors.yellow[400],
      'service-health-rate-fill': `color-mix(in srgb, ${colors.teal[400]} 24%, transparent)`,
    },
  };
})();

const statusColor = (status: ServiceHealthStatus): PillColors => ({
  bg: `var(--service-health-status-${status}-bg)`,
  bgHover: `var(--service-health-status-${status}-bg)`,
  text: `var(--service-health-status-${status}-text)`,
  textHover: `var(--service-health-status-${status}-text)`,
  focusOutline: `var(--service-health-status-${status}-text)`,
});

export const statusPillColors: Record<ServiceHealthStatus, PillColors> = {
  healthy: statusColor('healthy'),
  degraded: statusColor('degraded'),
  unknown: statusColor('unknown'),
};

export const getServiceHealthStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();

  return {
    overview: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[5],
      minWidth: 0,
    }),
    rangeGroup: css({
      display: 'flex',
      alignItems: 'center',
      gap: spacing[1],
      minWidth: 0,
      flexWrap: 'wrap',
    }),
    rangeLoading: css({
      display: 'inline-flex',
      width: spacing[4],
      height: spacing[4],
      alignItems: 'center',
      justifyContent: 'center',
    }),
    metrics: css({
      display: 'grid',
      gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
      gap: spacing[3],
      minWidth: 0,
      '@container (max-width: 560px)': {
        gridTemplateColumns: '1fr',
      },
    }),
    metric: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[3],
      minWidth: 0,
      margin: 0,
      padding: spacing[4],
      borderRadius: borderRadius.md,
      backgroundColor: 'var(--service-health-panel-surface)',
    }),
    metricHeader: css({
      display: 'flex',
      alignItems: 'baseline',
      justifyContent: 'space-between',
      gap: spacing[2],
      minWidth: 0,
    }),
    metricLabel: css({
      minWidth: 0,
      color: colors.interactive.mutedForeground,
      overflowWrap: 'anywhere',
    }),
    metricValue: css({
      flex: '0 0 auto',
      color: colors.surface.cardForeground,
      fontSize: typography.fontSize.ui.md,
      fontVariantNumeric: 'tabular-nums',
      whiteSpace: 'nowrap',
    }),
    chart: css({
      display: 'block',
      width: '100%',
      height: spacing[12],
      overflow: 'visible',
    }),
    latencyLine: css({ stroke: 'var(--service-health-latency-line)' }),
    latencyPoint: css({ fill: 'var(--service-health-latency-line)' }),
    errorLine: css({ stroke: 'var(--service-health-error-line)' }),
    errorPoint: css({ fill: 'var(--service-health-error-line)' }),
    errorFill: css({ fill: 'var(--service-health-error-fill)' }),
    rateLine: css({ stroke: 'var(--service-health-rate-line)' }),
    ratePoint: css({ fill: 'var(--service-health-rate-line)' }),
    rateFill: css({ fill: 'var(--service-health-rate-fill)' }),
    chartLine: css({
      fill: 'none',
      strokeWidth: 2,
      vectorEffect: 'non-scaling-stroke',
      strokeLinejoin: 'round',
      strokeLinecap: 'round',
    }),
    chartAxis: css({
      display: 'flex',
      justifyContent: 'space-between',
      gap: spacing[2],
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.xs,
      lineHeight: '16px',
    }),
    unavailable: css({
      display: 'grid',
      minHeight: spacing[12],
      placeItems: 'center',
      color: colors.interactive.mutedForeground,
      textAlign: 'center',
    }),
    dependencyRegion: css({
      minWidth: 0,
      overflowX: 'auto',
    }),
    dependencyTable: css({
      width: '100%',
      borderCollapse: 'collapse',
      tableLayout: 'fixed',
    }),
    dependencyCaption: css({
      paddingBlockEnd: spacing[2],
      color: colors.interactive.mutedForeground,
      textAlign: 'start',
    }),
    dependencyHeader: css({
      position: 'absolute',
      width: '1px',
      height: '1px',
      padding: 0,
      margin: '-1px',
      overflow: 'hidden',
      clip: 'rect(0, 0, 0, 0)',
      whiteSpace: 'nowrap',
      border: 0,
    }),
    dependencyRow: css({
      borderBlockEnd: `${borderWidth.thin} solid ${colors.line.border}`,
      '&:last-child': { borderBlockEnd: 0 },
    }),
    dependencyName: css({
      width: '50%',
      padding: `${spacing[2]} ${spacing[2]} ${spacing[2]} 0`,
      color: colors.surface.cardForeground,
      fontWeight: typography.fontWeight.normal,
      textAlign: 'start',
      overflowWrap: 'anywhere',
    }),
    dependencyNameDetail: css({
      display: 'block',
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.xs,
      fontWeight: typography.fontWeight.normal,
      lineHeight: '16px',
    }),
    dependencyValue: css({
      width: '25%',
      padding: `${spacing[2]} 0 ${spacing[2]} ${spacing[2]}`,
      color: colors.interactive.mutedForeground,
      fontVariantNumeric: 'tabular-nums',
      textAlign: 'end',
      whiteSpace: 'nowrap',
    }),
    dependencyUnavailable: css({
      padding: `${spacing[2]} 0`,
      color: colors.interactive.mutedForeground,
      textAlign: 'start',
    }),
    empty: css({
      margin: 0,
      color: colors.interactive.mutedForeground,
    }),
    warnings: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[1],
      color: colors.interactive.mutedForeground,
    }),
    warningHeading: css({
      margin: 0,
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.medium,
      lineHeight: '18px',
    }),
    warningList: css({
      margin: 0,
      paddingInlineStart: spacing[5],
    }),
    actions: css({
      display: 'flex',
      alignItems: 'center',
      gap: spacing[2],
      flexWrap: 'wrap',
      '@container (max-width: 360px)': {
        alignItems: 'stretch',
        flexDirection: 'column',
      },
    }),
  };
};
