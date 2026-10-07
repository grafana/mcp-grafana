import { useEffect, useId, useRef, useState } from 'react';
import { ChartColumn, ChartLine, ChevronDown, Grid2x2, Hash, Table, Target } from 'lucide-react';

import type { VizKind } from './pickViz';
import { getRenderMetricsAppStyles } from './RenderMetricsApp.styles';

/**
 * Visualization chooser.
 *
 * Changing the view here is a *user* control. The default still comes from the
 * response shape — nothing the model sends selects a visualization — so this
 * offers only the kinds that suit the data in hand, plus Table, which can
 * represent any of them.
 */

export type VizPickerProps = {
  options: VizKind[];
  value: VizKind;
  onChange: (kind: VizKind) => void;
};

const VIZ = {
  timeseries: { label: 'Time series', Icon: ChartLine },
  bar: { label: 'Bar', Icon: ChartColumn },
  bullet: { label: 'Bullet', Icon: Target },
  stat: { label: 'Stat', Icon: Hash },
  heatmap: { label: 'Heatmap', Icon: Grid2x2 },
  table: { label: 'Table', Icon: Table },
} as const satisfies Record<VizKind, { label: string; Icon: typeof ChartLine }>;

export function vizLabel(kind: VizKind): string {
  return VIZ[kind].label;
}

export function VizPicker({ options, value, onChange }: VizPickerProps) {
  const styles = getRenderMetricsAppStyles();
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const listId = useId();
  const Current = VIZ[value].Icon;

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

  return (
    <div className={styles.picker} ref={wrapperRef}>
      <button
        type="button"
        className={styles.controlButton}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        onClick={() => setOpen((shown) => !shown)}
      >
        <Current size={14} aria-hidden="true" />
        {VIZ[value].label}
        <ChevronDown size={14} aria-hidden="true" />
      </button>

      {open && (
        <ul className={styles.pickerMenu} id={listId} role="listbox" aria-label="Visualization">
          {options.map((kind) => {
            const { label, Icon } = VIZ[kind];
            return (
              <li key={kind}>
                <button
                  type="button"
                  className={styles.pickerOption}
                  role="option"
                  aria-selected={kind === value}
                  onClick={() => {
                    onChange(kind);
                    setOpen(false);
                  }}
                >
                  <Icon size={14} aria-hidden="true" />
                  {label}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
