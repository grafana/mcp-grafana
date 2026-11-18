import { cx } from '@emotion/css';
import { ChevronDown } from 'lucide-react';
import { type ReactNode, useEffect, useId, useRef, useState } from 'react';

import { Button } from './design';
import { getPickerStyles, PICKER_MENU_MIN_WIDTH } from './Picker.styles';

export type PickerOption<T extends string> = { value: T; label: string; icon?: ReactNode };

export type PickerProps<T extends string> = {
  /** The name of the list, for assistive technology. */
  label: string;
  options: Array<PickerOption<T>>;
  value: T;
  onChange: (value: T) => void;
  /** The name of the trigger. With none, the trigger is named by the label of the value. */
  triggerLabel?: string;
};

/**
 * A compact dropdown: one trigger that shows the value, and a listbox of the options. A dropdown keeps a control row
 * to one button in a narrow chat panel.
 */
export function Picker<T extends string>({ label, options, value, onChange, triggerLabel }: PickerProps<T>) {
  const styles = getPickerStyles();
  const [open, setOpen] = useState(false);
  // The menu opens under the right edge of the trigger. A trigger near the left edge of the frame, like one in a
  // header that wrapped, opens it under its left edge instead, so the menu stays in the frame.
  const [opensRight, setOpensRight] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const listId = useId();
  const current = options.find((option) => option.value === value);

  // Dismiss on outside click or Escape, so the menu never strands the user.
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!wrapperRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  const toggle = () => {
    // Measured on open only: one layout read per click, none per render.
    if (!open) setOpensRight((wrapperRef.current?.getBoundingClientRect().right ?? 0) < PICKER_MENU_MIN_WIDTH);
    setOpen(!open);
  };

  return (
    <div className={styles.picker} ref={wrapperRef}>
      <Button
        variant="secondary"
        size="xs"
        aria-label={triggerLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        onClick={toggle}
      >
        {current?.icon}
        {current?.label ?? value}
        <ChevronDown size={12} aria-hidden="true" />
      </Button>

      {open && (
        <ul className={cx(styles.menu, opensRight && styles.menuStart)} id={listId} role="listbox" aria-label={label}>
          {options.map((option) => (
            // A listbox holds only options, so the list item has no role of its own.
            <li key={option.value} role="none">
              <Button
                variant="ghost"
                size="xs"
                className={styles.option}
                role="option"
                aria-selected={option.value === value}
                onClick={() => {
                  onChange(option.value);
                  setOpen(false);
                }}
              >
                {option.icon}
                {option.label}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
