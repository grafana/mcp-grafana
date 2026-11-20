import type { GrafanaTheme2 } from '@grafana/data';

import type { getDesignTokens } from '../../src/design';

export type CodeTokens = ReturnType<typeof getDesignTokens>;

/**
 * One design role and the code token that plays it. The Freya value is read from the Visual Refresh theme in
 * `@grafana/data`, and the code value from the `--mcp-*` variable (or `getDesignTokens()`), so neither side is copied.
 */
export type TokenRow = {
  group: string;
  role: string;
  kind: 'color' | 'text';
  freya: (theme: GrafanaTheme2) => string;
  /** The CSS variable that plays this role in code. */
  mcpVar?: string;
  /** A value from getDesignTokens() when the role is not a CSS variable. */
  code?: (tokens: CodeTokens) => string;
  /** What the code has no token for, or why the two differ beyond their values. */
  note?: string;
};

const BASE_REM = 14;
/** Freya reports type sizes in rem on a 14px root. */
const px = (v: string | number) => {
  const s = String(v);
  return s.endsWith('rem') ? `${Math.round(parseFloat(s) * BASE_REM * 100) / 100}px` : /^\d+(\.\d+)?$/.test(s) ? `${s}px` : s;
};

const status = (name: 'success' | 'error' | 'warning' | 'info', mcp: { main?: string; text?: string }): TokenRow[] => [
  { group: `Status: ${name}`, role: `${name} main`, kind: 'color', freya: (t) => t.colors[name].main, mcpVar: mcp.main },
  { group: `Status: ${name}`, role: `${name} text`, kind: 'color', freya: (t) => t.colors[name].text, mcpVar: mcp.text },
  {
    group: `Status: ${name}`,
    role: `${name} subtle background`,
    kind: 'color',
    freya: (t) => t.colors[name].subtleBackground,
    note: 'Code builds this with color-mix(…15%, transparent) inside components; there is no token.',
  },
  {
    group: `Status: ${name}`,
    role: `${name} subtle border`,
    kind: 'color',
    freya: (t) => t.colors[name].subtleBorder,
    note: 'Code alerts have no border.',
  },
];

export const TOKEN_ROWS: TokenRow[] = [
  { group: 'Backgrounds', role: 'canvas', kind: 'color', freya: (t) => t.colors.background.canvas, note: 'The shell is the outermost layer in code.' },
  { group: 'Backgrounds', role: 'primary (card)', kind: 'color', freya: (t) => t.colors.background.primary, mcpVar: '--mcp-background' },
  { group: 'Backgrounds', role: 'secondary (inset)', kind: 'color', freya: (t) => t.colors.background.secondary, mcpVar: '--mcp-inset' },

  { group: 'Borders', role: 'weak', kind: 'color', freya: (t) => t.colors.border.weak, mcpVar: '--mcp-border' },
  { group: 'Borders', role: 'medium', kind: 'color', freya: (t) => t.colors.border.medium, mcpVar: '--mcp-border', note: 'Code has one border token for the weak and medium tiers.' },
  { group: 'Borders', role: 'strong', kind: 'color', freya: (t) => t.colors.border.strong, mcpVar: '--mcp-input-border' },

  { group: 'Text', role: 'primary', kind: 'color', freya: (t) => t.colors.text.primary, mcpVar: '--mcp-foreground' },
  { group: 'Text', role: 'secondary', kind: 'color', freya: (t) => t.colors.text.secondary, mcpVar: '--mcp-muted-foreground' },

  { group: 'Primary action', role: 'primary fill', kind: 'color', freya: (t) => t.colors.primary.main, mcpVar: '--mcp-primary', note: 'Design decision: Freya primary is neutral and inverted; code primary is blue.' },
  { group: 'Primary action', role: 'primary text', kind: 'color', freya: (t) => t.colors.primary.contrastText, mcpVar: '--mcp-primary-text' },

  ...status('success', { main: '--mcp-success', text: '--mcp-success-text' }),
  ...status('error', { main: '--mcp-error', text: '--mcp-error-text' }),
  ...status('warning', {}),
  ...status('info', { main: '--mcp-info', text: '--mcp-info-text' }),

  { group: 'Radii', role: 'sm', kind: 'text', freya: (t) => px(t.shape.radius.sm), code: (d) => d.primitives.borderRadius.sm },
  { group: 'Radii', role: 'md / default', kind: 'text', freya: (t) => px(t.shape.radius.default), code: (d) => d.primitives.borderRadius.md, note: 'Shell card uses a literal 16px and buttons a literal 6px.' },
  { group: 'Radii', role: 'lg', kind: 'text', freya: (t) => px(t.shape.radius.lg), note: 'Code has no lg radius.' },

  { group: 'Spacing (same key)', role: 'key 1', kind: 'text', freya: (t) => t.spacing(1), code: (d) => d.primitives.spacing[1], note: 'Freya key 1 is 8px; code key 1 is 4px.' },
  { group: 'Spacing (same key)', role: 'key 2', kind: 'text', freya: (t) => t.spacing(2), code: (d) => d.primitives.spacing[2], note: 'Aliasing the keys would double every gap.' },
  { group: 'Spacing (same key)', role: 'key 3', kind: 'text', freya: (t) => t.spacing(3), code: (d) => d.primitives.spacing[3] },

  { group: 'Typography', role: 'sans family', kind: 'text', freya: (t) => t.typography.fontFamily, code: (d) => d.primitives.typography.fontFamily.ui },
  { group: 'Typography', role: 'mono family', kind: 'text', freya: (t) => t.typography.fontFamilyMonospace, code: (d) => d.primitives.typography.fontFamily.monospace },
  { group: 'Typography', role: 'body / h5 size', kind: 'text', freya: (t) => px(t.typography.body.fontSize ?? t.typography.fontSize), code: (d) => d.primitives.typography.fontSize.ui.sm, note: 'Design text is 14px; code UI text is 12px.' },
  { group: 'Typography', role: 'small size', kind: 'text', freya: (t) => px(t.typography.bodySmall.fontSize ?? 12), code: (d) => d.primitives.typography.fontSize.ui.xs, note: 'Code has no line-heights, only sizes.' },
];

/** The same role in both themes, ready to render. */
export const GROUPS = [...new Set(TOKEN_ROWS.map((r) => r.group))];
