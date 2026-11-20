import { ChartColumn, ChartLine, Grid2x2, Hash, Table, Target } from 'lucide-react';

import { Picker } from '../Picker';
import type { VizKind } from './pickViz';

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
  return (
    <Picker
      label="Visualization"
      options={options.map((kind) => {
        const { label, Icon } = VIZ[kind];
        return { value: kind, label, icon: <Icon size={12} aria-hidden="true" /> };
      })}
      value={value}
      onChange={onChange}
    />
  );
}
