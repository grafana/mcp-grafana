import type { ReactNode } from 'react';

import { COMPONENTS, type ComponentDeltas, type Owner, SIZE_ORDER, type Size } from './deltas';
import { Badge, h2, muted, panel } from './ui';

export const SIZE_COLOR: Record<Size, string> = { large: '#c23b4d', medium: '#a6760a', small: '#2f6f9f' };
export const OWNER_COLOR: Record<Owner, string> = { repo: '#4b5563', renderer: '#7c3aed', decision: '#c2410c' };
export const OWNER_TEXT: Record<Owner, string> = {
  repo: 'this repo',
  renderer: 'Renderer / server',
  decision: 'needs a decision',
};

const cell = { padding: '6px 8px', borderBottom: '1px solid var(--mcp-border)', verticalAlign: 'top' } as const;

export function countBySize(c: ComponentDeltas): Record<Size, number> {
  const out: Record<Size, number> = { large: 0, medium: 0, small: 0 };
  for (const d of c.deltas) out[d.size] += 1;
  return out;
}

export function DeltaTable({ component }: { component: ComponentDeltas }) {
  const rows = [...component.deltas].sort((a, b) => SIZE_ORDER.indexOf(a.size) - SIZE_ORDER.indexOf(b.size));
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
      <thead>
        <tr style={{ textAlign: 'left', ...muted }}>
          <th style={cell}>Aspect</th>
          <th style={cell}>Design</th>
          <th style={cell}>Code today</th>
          <th style={cell}>Gap</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((d) => (
          <tr key={d.aspect}>
            <td style={{ ...cell, fontWeight: 600 }}>{d.aspect}</td>
            <td style={cell}>{d.design}</td>
            <td style={cell}>{d.code}</td>
            <td style={cell}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}>
                <Badge color={SIZE_COLOR[d.size]}>{d.size}</Badge>
                <Badge color={OWNER_COLOR[d.owner]}>{OWNER_TEXT[d.owner]}</Badge>
              </div>
              {d.note ? <div style={{ ...muted, fontSize: 12, marginTop: 4 }}>{d.note}</div> : null}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** One component's page: the live component on the left, and the table of what separates it from the design. */
export function DeltaView({ id, children }: { id: string; children: ReactNode }) {
  const component = COMPONENTS.find((c) => c.id === id);
  if (!component) throw new Error(`unknown delta component: ${id}`);
  const counts = countBySize(component);
  const renderer = component.deltas.some((d) => d.owner === 'renderer');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <header style={panel}>
        <h2 style={h2}>{component.title}</h2>
        <p style={{ margin: '0 0 8px' }}>{component.summary}</p>
        <p style={{ ...muted, margin: '0 0 4px' }}>
          <b>Code today:</b> {component.counterpart}
        </p>
        <p style={{ ...muted, margin: '0 0 8px' }}>
          <b>Design boards:</b> {component.artboards.join(' · ')}
        </p>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {SIZE_ORDER.map((s) => (
            <span key={s}>
              <Badge color={SIZE_COLOR[s]}>{counts[s]}</Badge> {s}
            </span>
          ))}
        </div>
        {renderer ? (
          <p style={{ ...muted, margin: '8px 0 0' }}>
            Some of this is drawn by the external viz Renderer, which this repo can theme only as light or dark.
            Those rows are marked <i>Renderer / server</i>.
          </p>
        ) : null}
      </header>
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'flex-start' }}>
        <section aria-label="Current component" style={{ flex: '1 1 460px', minWidth: 0 }}>
          <p
            style={{
              ...muted,
              margin: '0 0 8px',
              fontFamily: 'Inter, system-ui, sans-serif',
              fontSize: 12,
              textTransform: 'uppercase',
              letterSpacing: '.04em',
            }}
          >
            Code today
          </p>
          {children}
        </section>
        <section aria-label="Delta table" style={{ ...panel, flex: '1 1 560px', minWidth: 0 }}>
          <DeltaTable component={component} />
        </section>
      </div>
    </div>
  );
}
