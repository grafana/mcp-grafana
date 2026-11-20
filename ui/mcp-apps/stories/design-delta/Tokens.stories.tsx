import { getThemeById } from '@grafana/data';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { useLayoutEffect, useMemo, useRef, useState } from 'react';

import { getDesignTokens } from '../../src/design';
import { CLOSE_MAX, MATCH_MAX, type Verdict, classifyColor } from './colorMath';
import { GROUPS, TOKEN_ROWS, type TokenRow } from './tokenMap';
import { Badge, VERDICT_COLOR, VERDICT_TEXT, h2, muted, panel } from './ui';

const meta: Meta = {
  title: 'Design delta/Tokens',
  parameters: {
    docs: {
      description: {
        component:
          'Freya (the Visual Refresh theme in @grafana/data) against the --mcp-* tokens the App ships. Both sides ' +
          'are read live, so nothing here is copied and the page cannot go stale.',
      },
    },
  },
};
export default meta;

type Mode = 'light' | 'dark';
type Result = { row: TokenRow; freya: string; code?: string; verdict: Verdict; distance?: number };

const THEME_ID: Record<Mode, string> = { dark: 'visual_refresh_dark', light: 'visual_refresh_light' };

function compare(mode: Mode, read: (name: string) => string): Result[] {
  const theme = getThemeById(THEME_ID[mode]);
  const tokens = getDesignTokens();
  const base = theme.colors.background.primary;
  return TOKEN_ROWS.map((row) => {
    const freya = row.freya(theme);
    const code = row.mcpVar ? read(row.mcpVar) || undefined : row.code?.(tokens);
    if (row.kind === 'color') return { row, freya, code, ...classifyColor(freya, code, base) };
    const verdict: Verdict = code === undefined ? 'missing' : code === freya ? 'match' : 'different';
    return { row, freya, code, verdict };
  });
}

function Swatch({ value }: { value?: string }) {
  return (
    <span
      aria-hidden="true"
      style={{
        display: 'inline-block',
        width: 18,
        height: 18,
        borderRadius: 4,
        border: '1px solid var(--mcp-border)',
        verticalAlign: 'middle',
        background: value ?? 'repeating-linear-gradient(45deg, transparent 0 4px, var(--mcp-border) 4px 5px)',
      }}
    />
  );
}

const cell = { padding: '6px 8px', borderBottom: '1px solid var(--mcp-border)', verticalAlign: 'top' } as const;

function Column({ mode }: { mode: Mode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [results, setResults] = useState<Result[]>([]);
  useLayoutEffect(() => {
    // The --mcp-* variables are scoped by data-color-mode, so read them from this column's own element.
    const style = getComputedStyle(ref.current!);
    setResults(compare(mode, (name) => style.getPropertyValue(name).trim()));
  }, [mode]);
  const counts = useMemo(() => {
    const c: Record<Verdict, number> = { match: 0, close: 0, different: 0, missing: 0 };
    for (const r of results) c[r.verdict] += 1;
    return c;
  }, [results]);

  return (
    <section ref={ref} data-color-mode={mode} style={{ ...panel, flex: '1 1 520px', minWidth: 0 }} aria-label={`${mode} theme`}>
      <h2 style={h2}>{mode === 'dark' ? 'Dark' : 'Light'}</h2>
      <p style={{ ...muted, margin: '0 0 12px' }}>
        {(Object.keys(counts) as Verdict[]).map((v) => (
          <span key={v} style={{ marginRight: 12 }}>
            <Badge color={VERDICT_COLOR[v]}>{counts[v]}</Badge> {VERDICT_TEXT[v]}
          </span>
        ))}
      </p>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr style={{ textAlign: 'left', ...muted }}>
            <th style={cell}>Role</th>
            <th style={cell}>Freya</th>
            <th style={cell}>Code</th>
            <th style={cell}>Verdict</th>
          </tr>
        </thead>
        <tbody>
          {GROUPS.map((group) => (
            <GroupRows key={group} group={group} results={results.filter((r) => r.row.group === group)} />
          ))}
        </tbody>
      </table>
    </section>
  );
}

function GroupRows({ group, results }: { group: string; results: Result[] }) {
  if (!results.length) return null;
  return (
    <>
      <tr>
        <th colSpan={4} style={{ ...cell, paddingTop: 14, textAlign: 'left', fontSize: 12, textTransform: 'uppercase', letterSpacing: '.04em', ...muted }}>
          {group}
        </th>
      </tr>
      {results.map(({ row, freya, code, verdict, distance }) => (
        <tr key={`${group}-${row.role}`}>
          <td style={cell}>
            {row.role}
            {row.note ? <div style={{ ...muted, fontSize: 12 }}>{row.note}</div> : null}
          </td>
          <td style={cell}>
            {row.kind === 'color' ? <Swatch value={freya} /> : null} <code style={{ fontSize: 12 }}>{freya}</code>
          </td>
          <td style={cell}>
            {row.kind === 'color' ? <Swatch value={code} /> : null}{' '}
            <code style={{ fontSize: 12 }}>{code ?? 'none'}</code>
            {row.mcpVar ? <div style={{ ...muted, fontSize: 11 }}>{row.mcpVar}</div> : null}
          </td>
          <td style={cell}>
            <Badge color={VERDICT_COLOR[verdict]} title={distance === undefined ? undefined : `OKLab distance ${distance.toFixed(3)}`}>
              {VERDICT_TEXT[verdict]}
            </Badge>
            {distance === undefined ? null : <div style={{ ...muted, fontSize: 11 }}>ΔE {distance.toFixed(3)}</div>}
          </td>
        </tr>
      ))}
    </>
  );
}

export const FreyaVsCode: StoryObj = {
  name: 'Freya vs code tokens',
  render: () => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <p style={{ margin: 0, maxWidth: 820 }}>
        Colors are compared by distance in OKLab: <b>match</b> up to {MATCH_MAX}, <b>close</b> up to {CLOSE_MAX},{' '}
        <b>different</b> above that, so a close pair is a tweak and a different pair is a restyle. Radii, spacing and
        type are compared as text. Spacing is compared by the <i>same key</i> on purpose: Freya&apos;s unit is 8px
        and the code&apos;s is 4px, so aliasing the keys would double every gap.
      </p>
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'flex-start' }}>
        <Column mode="light" />
        <Column mode="dark" />
      </div>
    </div>
  ),
};
