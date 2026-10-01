import { css } from '@emotion/css';
import { getDesignTokens } from '../src/design';

export const getStyles = () => {
  const {
    primitives: { spacing, typography, borderRadius },
    semantic: { colors },
  } = getDesignTokens();
  return {
    page: css({
      margin: 0,
      padding: spacing[5],
      fontFamily: typography.fontFamily.ui,
      fontSize: typography.fontSize.ui.sm,
      lineHeight: 1.5,
      background: colors.surface.background,
      color: colors.surface.foreground,
      minHeight: '100vh',
      boxSizing: 'border-box',
    }),
    heading: css({ margin: 0, fontSize: typography.fontSize.ui.lg, fontWeight: typography.fontWeight.medium }),
    description: css({ margin: `${spacing[2]} 0`, color: colors.interactive.mutedForeground }),
    toolbar: css({ display: 'flex', flexWrap: 'wrap', gap: spacing[2], marginBottom: spacing[6] }),
    examples: css({ display: 'flex', flexWrap: 'wrap', gap: spacing[6], alignItems: 'flex-start' }),
    example: css({ flex: '1 1 360px', minWidth: 0, maxWidth: 720 }), // Figma app measure; independent of host viewport.
    label: css({
      margin: `0 0 ${spacing[3]}`,
      fontSize: typography.fontSize.ui.sm,
      fontWeight: typography.fontWeight.medium,
    }),
    surface: css({
      background: colors.interactive.muted,
      borderRadius: borderRadius.md,
      padding: spacing[5],
      display: 'flex',
      flexWrap: 'wrap',
      gap: spacing[3],
      alignItems: 'center',
      justifyContent: 'space-between',
    }),
    note: css({ margin: `${spacing[6]} 0 0`, color: colors.interactive.mutedForeground }),
  };
};
