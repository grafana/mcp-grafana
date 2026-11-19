import { css } from '@emotion/css';
import { getDesignTokens } from './design';

/** Styles for `QueryBlock`, per the package's styles-factory convention. */
export const getQueryBlockStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();

  const code = {
    fontFamily: typography.fontFamily.monospace,
    fontSize: typography.fontSize.monospace.sm,
    lineHeight: '20px',
  };

  return {
    block: css({
      display: 'flex',
      flexDirection: 'column',
      gap: spacing[2],
      minWidth: 0,
      padding: spacing[3],
      borderRadius: borderRadius.md,
      border: `${borderWidth.thin} solid ${colors.line.border}`,
      // The surface of the summary card that the block replaces; McpAppShell sets it.
      background: 'var(--mcp-app-shell-content-surface)',
    }),

    header: css({ display: 'flex', alignItems: 'center', gap: spacing[1], minWidth: 0 }),

    /** An icon-only ghost Button. */
    iconButton: css({ flex: 'none', width: spacing[6], padding: 0 }),

    /** Copy sits at the end of the header. */
    copy: css({ marginLeft: 'auto' }),

    chip: css({
      display: 'inline-flex',
      alignItems: 'center',
      gap: spacing[1],
      minWidth: 0,
      padding: `2px ${spacing[2]}`,
      borderRadius: borderRadius.md,
      border: `${borderWidth.thin} solid ${colors.line.border}`,
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.sm,
    }),

    chipText: css({
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
      color: colors.surface.foreground,
      fontWeight: typography.fontWeight.medium,
    }),

    /** The collapsed block: the queries on one line, cut at the edge. */
    preview: css({
      ...code,
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
      color: colors.interactive.mutedForeground,
    }),

    queries: css({ display: 'flex', flexDirection: 'column', gap: spacing[2], minWidth: 0 }),

    refId: css({
      marginBottom: spacing[1],
      color: colors.interactive.mutedForeground,
      fontSize: typography.fontSize.ui.xs,
      fontWeight: typography.fontWeight.medium,
    }),

    /** A query as written: its line breaks stay, and a long line wraps rather than scrolls in a narrow chat panel. */
    code: css({
      ...code,
      margin: 0,
      padding: spacing[2],
      borderRadius: borderRadius.md,
      background: colors.surface.background,
      color: colors.surface.foreground,
      whiteSpace: 'pre-wrap',
      // Break inside a long metric name, not after the indent before it, which would leave a blank line.
      wordBreak: 'break-all',
    }),

    /** Read by assistive technology only. */
    srOnly: css({
      position: 'absolute',
      width: 1,
      height: 1,
      overflow: 'hidden',
      clipPath: 'inset(50%)',
      whiteSpace: 'nowrap',
    }),
  };
};
