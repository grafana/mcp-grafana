import { cx } from '@emotion/css';
import { ChevronDown } from 'lucide-react';
import { type ReactNode, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';

import { Button } from './design';
import { getPickerStyles } from './Picker.styles';

export type PickerOption<T extends string> = { value: T; label: string; icon?: ReactNode };

export type PickerProps<T extends string> = {
  /** The accessible name of the list. */
  label: string;
  options: Array<PickerOption<T>>;
  value: T;
  onChange: (value: T) => void;
  /** The accessible name of the trigger; defaults to the label of the value. */
  triggerLabel?: string;
};

/** A dropdown of one trigger and a listbox, so a control row in a narrow chat panel stays one button. */
export function Picker<T extends string>({ label, options, value, onChange, triggerLabel }: PickerProps<T>) {
  const styles = getPickerStyles();
  const [open, setOpen] = useState(false);
  // Opens to the right when the menu doesn't fit left of the trigger, as in a wrapped header.
  const [opensRight, setOpensRight] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLUListElement>(null);
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

  // Measured before paint; the menu's width doesn't depend on its side.
  useLayoutEffect(() => {
    const menu = menuRef.current;
    const wrapper = wrapperRef.current;
    if (open && menu && wrapper) setOpensRight(menu.offsetWidth > wrapper.getBoundingClientRect().right);
  }, [open]);

  return (
    <div className={styles.picker} ref={wrapperRef}>
      <Button
        variant="secondary"
        size="xs"
        aria-label={triggerLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        onClick={() => setOpen(!open)}
      >
        {current?.icon}
        {current?.label ?? value}
        <ChevronDown size={12} aria-hidden="true" />
      </Button>

      {open && (
        <ul
          ref={menuRef}
          className={cx(styles.menu, opensRight && styles.menuStart)}
          id={listId}
          role="listbox"
          aria-label={label}
        >
          {options.map((option) => (
            // A listbox holds only options.
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
