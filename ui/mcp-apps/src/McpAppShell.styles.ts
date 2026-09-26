import { css } from '@emotion/css';
import { type CSSVariablesByColorMode, getDesignTokens } from './design';

export const mcpAppShellCSSVariables: CSSVariablesByColorMode = (() => {
  const {
    primitives: { colors },
  } = getDesignTokens();

  return {
    light: {
      'mcp-app-shell-summary-label': colors.brandOrange[700],
      // Quiet app-content surface; candidate for a semantic design token.
      'mcp-app-shell-content-surface': colors.neutral[50],
    },
    dark: {
      'mcp-app-shell-summary-label': colors.brandOrange[400],
      // Quiet app-content surface; candidate for a semantic design token.
      'mcp-app-shell-content-surface': colors.smoke[900],
    },
  };
})();

export const getMcpAppShellStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();

  return {
    shell: css({
      boxSizing: 'border-box',
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[3],
      width: '100%',
      maxWidth: '720px', // The MCP Apps shell has a fixed reference measure, not a design-token role.
      minWidth: 0,
      padding: spacing[5],
      border: `${borderWidth.thin} solid ${colors.line.border}`,
      borderRadius: '16px', // No radius token resolves to the Figma frame's exact 16px radius (xl is 14px; 2xl is 18px).
      backgroundColor: colors.surface.background,
      color: colors.surface.foreground,
      fontFamily: typography.fontFamily.ui,
      fontSize: typography.fontSize.ui.sm,
      lineHeight: '18px', // The 12/18 MCP Apps body role is not yet represented by a line-height token.
      containerType: 'inline-size',
    }),
    wide: css({ maxWidth: '1280px' }), // Dense visualizations need room for an adjacent inspector.
    compact: css({
      padding: spacing[4],
      '@container (max-width: 480px)': { padding: spacing[3] },
    }),
    compactSummary: css({ padding: spacing[3] }),
    header: css({
      display: 'flex',
      alignItems: 'flex-start',
      justifyContent: 'space-between',
      gap: spacing[3],
      minWidth: 0,
      flexWrap: 'wrap',
    }),
    identity: css({
      display: 'flex',
      alignItems: 'center',
      gap: spacing[2],
      minWidth: 0,
      flex: '1 1 240px',
    }),
    logo: css({
      display: 'block',
      width: spacing[4],
      height: spacing[4],
      flex: '0 0 auto',
    }),
    identityText: css({
      display: 'flex',
      alignItems: 'baseline',
      gap: spacing[1],
      minWidth: 0,
      flexWrap: 'wrap',
      overflowWrap: 'anywhere',
    }),
    product: css({
      color: colors.surface.foreground,
      fontWeight: typography.fontWeight.semibold,
    }),
    separator: css({
      color: colors.interactive.mutedForegroundSubtle,
    }),
    scope: css({
      minWidth: 0,
      color: colors.interactive.mutedForeground,
      overflowWrap: 'anywhere',
    }),
    headerActions: css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'flex-end',
      gap: spacing[1],
      minWidth: 0,
      flex: '0 1 auto',
      flexWrap: 'wrap',
    }),
    summary: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[1],
      minWidth: 0,
      padding: `${spacing[4]} ${spacing[5]}`,
      borderRadius: borderRadius.md,
      backgroundColor: 'var(--mcp-app-shell-content-surface)',
      color: colors.surface.cardForeground,
    }),
    summaryHeader: css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      flexWrap: 'wrap',
      gap: spacing[2],
    }),
    summaryLabel: css({
      color: 'var(--mcp-app-shell-summary-label)',
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.medium,
      overflowWrap: 'anywhere',
    }),
    summaryTitle: css({
      margin: 0,
      color: colors.surface.cardForeground,
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.normal,
      lineHeight: '18px', // The compact heading role does not yet expose a line-height token.
      overflowWrap: 'anywhere',
    }),
    summaryDescription: css({
      margin: 0,
      color: colors.interactive.mutedForeground,
      overflowWrap: 'anywhere',
    }),
    content: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[3],
      minWidth: 0,
    }),
    footer: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[3],
      minWidth: 0,
    }),
    feedback: css({
      display: 'flex',
      alignItems: 'flex-start',
      gap: spacing[2],
      minWidth: 0,
      padding: `${spacing[2]} ${spacing[3]}`,
      borderRadius: borderRadius.md,
    }),
    feedbackSuccess: css({
      backgroundColor: `color-mix(in oklab, ${colors.status.success} 15%, transparent)`,
      color: colors.status.successSubtleForeground,
    }),
    feedbackError: css({
      backgroundColor: `color-mix(in oklab, ${colors.status.destructive} 15%, transparent)`,
      color: colors.status.destructiveSubtleForeground,
    }),
    feedbackInfo: css({
      backgroundColor: `color-mix(in oklab, ${colors.status.info} 15%, transparent)`,
      color: colors.status.infoSubtleForeground,
    }),
    feedbackIcon: css({
      display: 'inline-flex',
      alignItems: 'center',
      height: '18px', // Aligns a 16px icon to the MCP Apps 18px body line box.
      flex: '0 0 auto',
    }),
    feedbackMessage: css({
      minWidth: 0,
      flex: '1 1 auto',
      overflowWrap: 'anywhere',
    }),
    feedbackAction: css({
      flex: '0 0 auto',
      marginBlock: `calc(${spacing[1.5]} * -1)`,
    }),
    tip: css({
      display: 'flex',
      alignItems: 'flex-start',
      gap: spacing[2],
      minWidth: 0,
      padding: `${spacing[2]} ${spacing[3]}`,
      borderRadius: borderRadius.md,
      backgroundColor: colors.interactive.muted,
      color: colors.interactive.mutedForeground,
    }),
    actions: css({
      display: 'flex',
      justifyContent: 'flex-start',
      alignItems: 'center',
      gap: spacing[2],
      minWidth: 0,
      flexWrap: 'wrap',
    }),
    loadingIndicator: css({
      display: 'inline-flex',
      alignItems: 'center',
      justifyContent: 'center',
      width: spacing[4],
      height: spacing[4],
      flex: '0 0 auto',
    }),
    narrowHeaderActions: css({
      '@container (max-width: 360px)': {
        width: '100%',
        justifyContent: 'flex-start',
      },
    }),
    narrowFeedback: css({
      '@container (max-width: 360px)': {
        flexWrap: 'wrap',
      },
    }),
    narrowFeedbackAction: css({
      '@container (max-width: 360px)': {
        width: '100%',
        marginBlock: 0,
      },
    }),
    narrowActions: css({
      '@container (max-width: 360px)': {
        justifyContent: 'stretch',
        flexDirection: 'column',
        alignItems: 'stretch',
      },
    }),
  };
};
