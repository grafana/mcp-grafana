import { css } from '@emotion/css';
import { useEffect, useRef } from 'react';

import { getDesignTokens } from '../src/design';

const getStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();
  return {
    text: css({
      display: 'block',
      width: '100%',
      boxSizing: 'border-box',
      marginTop: spacing[2],
      padding: spacing[2],
      fontFamily: typography.fontFamily.monospace,
      fontSize: typography.fontSize.monospace.sm,
      color: colors.surface.foreground,
      background: colors.surface.background,
      border: `${borderWidth.thin} solid ${colors.line.border}`,
      borderRadius: borderRadius.sm,
      resize: 'vertical',
    }),
  };
};

/**
 * The error of a failed Ask in chat. With the text of the message, it shows the text in a box that the user cannot
 * edit, selected, so the user can copy it into the chat (state.27). It does not write to the clipboard.
 */
export function VizMessageError({ error, text }: { error: string; text?: string }) {
  const styles = getStyles();
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => box.current?.select(), [text]);
  return (
    <>
      {error}
      {text !== undefined && (
        <textarea ref={box} className={styles.text} readOnly rows={4} value={text} aria-label="Message text" />
      )}
    </>
  );
}
