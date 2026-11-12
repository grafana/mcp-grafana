import { css } from '@emotion/css';
import { type CSSVariablesByColorMode, getDesignTokens } from './design';

export const mcpAppSectionCSSVariables: CSSVariablesByColorMode = (() => {
  const {
    primitives: { colors },
  } = getDesignTokens();

  return {
    light: {
      'mcp-app-section-title': colors.brandOrange[700],
      // Quiet app-content surface; candidate for a semantic design token.
      'mcp-app-section-content-surface': colors.neutral[50],
    },
    dark: {
      'mcp-app-section-title': colors.brandOrange[400],
      // Quiet app-content surface; candidate for a semantic design token.
      'mcp-app-section-content-surface': colors.smoke[900],
    },
  };
})();

export const getMcpAppSectionStyles = () => {
  const {
    primitives: { borderRadius, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();

  return {
    section: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[4],
      minWidth: 0,
      padding: `${spacing[4]} ${spacing[5]}`,
      borderRadius: borderRadius.md,
      backgroundColor: 'var(--mcp-app-section-content-surface)',
      color: colors.surface.cardForeground,
    }),
    headingRow: css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      flexWrap: 'wrap',
      gap: spacing[3],
      minWidth: 0,
    }),
    headingGroup: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[1],
      minWidth: 0,
    }),
    title: css({
      margin: 0,
      color: 'var(--mcp-app-section-title)',
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.medium,
      lineHeight: '18px', // The 12/18 MCP Apps heading role is not yet represented by a line-height token.
      overflowWrap: 'anywhere',
    }),
    description: css({
      color: colors.interactive.mutedForeground,
      overflowWrap: 'anywhere',
    }),
    body: css({
      minWidth: 0,
      overflowWrap: 'anywhere',
    }),
    result: css({
      minWidth: 0,
      overflowWrap: 'anywhere',
    }),
  };
};
