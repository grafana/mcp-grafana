import type { ComponentProps, ReactNode } from 'react';

import { Button } from '../design';
import { getRenderMetricsAppStyles } from './RenderMetricsApp.styles';

type IconButtonProps = Omit<ComponentProps<typeof Button>, 'variant' | 'size' | 'children' | 'aria-label'> & {
  /** Accessible name; also the tooltip unless `tooltip` overrides it. */
  label: string;
  /** Tooltip text when it should differ from the name, e.g. to describe a pressed state. */
  tooltip?: string;
  icon: ReactNode;
};

/** A square, icon-only shell Button with a tooltip on hover and keyboard focus. */
export function IconButton({ label, tooltip, icon, className, ...props }: IconButtonProps) {
  const styles = getRenderMetricsAppStyles();
  return (
    <span className={styles.tooltip} data-tooltip={tooltip ?? label}>
      <Button variant="secondary" size="xs" className={styles.iconButton} aria-label={label} {...props}>
        {icon}
      </Button>
    </span>
  );
}
