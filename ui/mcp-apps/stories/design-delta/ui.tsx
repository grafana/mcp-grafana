import type { CSSProperties, ReactNode } from 'react';

import type { Verdict } from './colorMath';

/** Shared look of the delta pages. They use the App's own --mcp-* variables, so they follow the toolbar's color mode. */
export const VERDICT_COLOR: Record<Verdict, string> = {
  match: '#1f8a4c',
  close: '#a6760a',
  different: '#c23b4d',
  missing: '#6b7280',
};

export const VERDICT_TEXT: Record<Verdict, string> = {
  match: 'match',
  close: 'close',
  different: 'different',
  missing: 'missing in code',
};

export function Badge({ color, children, title }: { color: string; children: ReactNode; title?: string }) {
  return (
    <span
      title={title}
      style={{
        display: 'inline-block',
        padding: '1px 8px',
        borderRadius: 999,
        background: color,
        color: '#fff',
        fontSize: 11,
        fontWeight: 600,
        lineHeight: '16px',
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </span>
  );
}

export const panel: CSSProperties = {
  boxSizing: 'border-box',
  background: 'var(--mcp-background)',
  color: 'var(--mcp-foreground)',
  border: '1px solid var(--mcp-border)',
  borderRadius: 12,
  padding: 16,
  fontFamily: 'Inter, system-ui, sans-serif',
  fontSize: 13,
  lineHeight: '20px',
};

export const h2: CSSProperties = { margin: '0 0 4px', fontSize: 16, fontWeight: 600 };
export const muted: CSSProperties = { color: 'var(--mcp-muted-foreground)' };
