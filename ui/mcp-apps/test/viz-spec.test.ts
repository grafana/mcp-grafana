// @vitest-environment node
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

import { buildVizSpec, parseVizArgs, parseVizResult } from '../app/vizSpec';

// The App test of the viz contract (fixtures.9). The sets are copies of grafana/viz-contract/fixtures at the commit in
// tools/viz_contract/CONTRACT.
const root = join(__dirname, 'viz-contract');
const read = (set: string, file: string) => JSON.parse(readFileSync(join(root, set, file), 'utf8'));

describe('viz spec', () => {
  const sets = readdirSync(root);

  it('has App fixture sets', () => {
    expect(sets.length).toBeGreaterThan(0);
  });

  it.each(sets)('%s: the viz spec equals spec.json', (set) => {
    const args = parseVizArgs(read(set, 'args.json'));
    const result = parseVizResult(read(set, 'result.json'));
    expect(args).toBeDefined();
    expect(result).toBeDefined();
    expect(buildVizSpec(args!, result!)).toEqual(read(set, 'spec.json'));
  });

  const set = 'prometheus-line';

  it('copies the panel of the alternative that the user picked (spec.2)', () => {
    const result = parseVizResult(read(set, 'result.json'))!;
    const spec = buildVizSpec(parseVizArgs(read(set, 'args.json'))!, result, {}, 'barchart');
    const barchart = result.alternatives.find((a) => a.chart === 'barchart')!;
    expect(spec.panel).toMatchObject(barchart.panel);
  });

  it('keeps the chart of the result for a chart it does not have', () => {
    const result = parseVizResult(read(set, 'result.json'))!;
    const spec = buildVizSpec(parseVizArgs(read(set, 'args.json'))!, result, {}, 'sankey');
    expect(spec.panel.type).toBe(result.panel.type);
    expect(spec.panel.options).toEqual(result.panel.options);
  });

  it('gives the theme and the time zone of the host (spec.5, spec.6)', () => {
    const spec = buildVizSpec(parseVizArgs(read(set, 'args.json'))!, parseVizResult(read(set, 'result.json'))!, {
      theme: 'light',
      timeZone: 'Europe/Paris',
    });
    expect(spec.theme).toBe('light');
    expect(spec.timeZone).toBe('Europe/Paris');
  });

  it('reads no result from a payload of another tool', () => {
    expect(parseVizResult({ resultType: 'matrix', result: [] })).toBeUndefined();
    expect(parseVizArgs({ expr: 'up' })).toBeUndefined();
  });
});
