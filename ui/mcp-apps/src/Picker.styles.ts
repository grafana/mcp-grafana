import { css } from '@emotion/css';
import { getDesignTokens } from './design';

/** The least width of the menu. Picker opens the menu to the right when it does not fit left of the trigger. */
export const PICKER_MENU_MIN_WIDTH = 160;

/** Styles for `Picker`, per the package's styles-factory convention. */
export const getPickerStyles = () => {
  const {
    primitives: { borderRadius, borderWidth, spacing },
    semantic: { colors },
  } = getDesignTokens();

  return {
    picker: css({ position: 'relative' }),

    menu: css({
      position: 'absolute',
      top: `calc(100% + ${spacing[1]})`,
      right: 0,
      zIndex: 2,
      minWidth: PICKER_MENU_MIN_WIDTH,
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

    /** The menu under the left edge of the trigger, for a trigger near the left edge of the frame. */
    menuStart: css({ left: 0, right: 'auto' }),

    /** A ghost Button, stretched to a full-width menu row. */
    option: css({
      width: '100%',
      justifyContent: 'flex-start',
      '&[aria-selected="true"]': { background: colors.interactive.accent },
    }),
  };
};
