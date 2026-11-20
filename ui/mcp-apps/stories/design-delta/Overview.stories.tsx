import type { Meta, StoryObj } from '@storybook/react-vite';

import { OWNER_COLOR, OWNER_TEXT, SIZE_COLOR, countBySize } from './DeltaTable';
import { COMPONENTS, OPEN_DECISIONS, SIZE_ORDER } from './deltas';
import { Badge, h2, muted, panel } from './ui';

const meta: Meta = {
  title: 'Design delta/Overview',
  parameters: {
    docs: {
      description: {
        component: 'Where the code stands against the design canvas, per component, and the calls nobody can make alone.',
      },
    },
  },
};
export default meta;

const cell = { padding: '6px 8px', borderBottom: '1px solid var(--mcp-border)', verticalAlign: 'top' } as const;

export const Summary: StoryObj = {
  name: 'Gap by component',
  render: () => {
    const total = COMPONENTS.reduce(
      (acc, c) => {
        const n = countBySize(c);
        for (const s of SIZE_ORDER) acc[s] += n[s];
        return acc;
      },
      { large: 0, medium: 0, small: 0 },
    );
    const owners = COMPONENTS.flatMap((c) => c.deltas).reduce(
      (acc, d) => ({ ...acc, [d.owner]: acc[d.owner] + 1 }),
      { repo: 0, renderer: 0, decision: 0 },
    );
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 1000 }}>
        <section style={panel}>
          <h2 style={h2}>
            {COMPONENTS.length} components, {total.large + total.medium + total.small} deltas
          </h2>
          <p style={{ ...muted, margin: '0 0 8px' }}>
            Sizes are rough: <b>small</b> is a token or value tweak, <b>medium</b> a restyle or small addition, and{' '}
            <b>large</b> a new component or behaviour. Open a component page for the values, and the Tokens page for
            the colors, radii, spacing and type.
          </p>
          <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
            {SIZE_ORDER.map((s) => (
              <span key={s}>
                <Badge color={SIZE_COLOR[s]}>{total[s]}</Badge> {s}
              </span>
            ))}
            {(Object.keys(owners) as (keyof typeof owners)[]).map((o) => (
              <span key={o}>
                <Badge color={OWNER_COLOR[o]}>{owners[o]}</Badge> {OWNER_TEXT[o]}
              </span>
            ))}
          </div>
        </section>

        <section style={panel}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ textAlign: 'left', ...muted }}>
                <th style={cell}>Component</th>
                <th style={cell}>Large</th>
                <th style={cell}>Medium</th>
                <th style={cell}>Small</th>
                <th style={cell}>Code today</th>
              </tr>
            </thead>
            <tbody>
              {COMPONENTS.map((c) => {
                const n = countBySize(c);
                return (
                  <tr key={c.id}>
                    <td style={{ ...cell, fontWeight: 600 }}>{c.title}</td>
                    {SIZE_ORDER.map((s) => (
                      <td key={s} style={cell}>
                        {n[s] ? <Badge color={SIZE_COLOR[s]}>{n[s]}</Badge> : <span style={muted}>0</span>}
                      </td>
                    ))}
                    <td style={{ ...cell, ...muted }}>{c.counterpart}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>

        <section style={panel}>
          <h2 style={h2}>Open decisions</h2>
          <p style={{ ...muted, margin: '0 0 8px' }}>These need a design call before the matching rows can be closed.</p>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ textAlign: 'left', ...muted }}>
                <th style={cell}>Decision</th>
                <th style={cell}>Design</th>
                <th style={cell}>Code today</th>
              </tr>
            </thead>
            <tbody>
              {OPEN_DECISIONS.map((d) => (
                <tr key={d.title}>
                  <td style={{ ...cell, fontWeight: 600 }}>{d.title}</td>
                  <td style={cell}>{d.design}</td>
                  <td style={cell}>{d.code}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      </div>
    );
  },
};
