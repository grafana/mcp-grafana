import { cache } from '@emotion/css';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterAll, describe, expect, it, vi } from 'vitest';

import { exposeSharedModules, SHARED_MODULES_GLOBAL } from '../app/sharedModules';
import { readVizFixture } from './vizFixtures';

type Renderer = typeof import('../renderer/renderer.js');

// uPlot, which @grafana/ui loads, reads matchMedia at import; jsdom has none.
window.matchMedia ??= (() => ({ matches: false, addEventListener() {}, removeEventListener() {} })) as never;

const global = globalThis as Record<string, unknown>;

afterAll(() => {
  delete global.__grafanaVizRenderer;
  delete global[SHARED_MODULES_GLOBAL];
});

// The built resource as the App will run it (#1589): a classic script, after the App puts the shared packages on the
// global. jsdom has no canvas or layout, so the chart does not reach the ready state; the legend shows the draw.
describe('dist/viz-renderer.js', () => {
  it('sets the Renderer global, adds its CSS and draws a fixture with the App @emotion/css and no request', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch');
    exposeSharedModules(globalThis);
    (0, eval)(readFileSync(join(__dirname, '..', 'dist', 'viz-renderer.js'), 'utf8'));

    const renderer = global.__grafanaVizRenderer as Renderer;
    expect(renderer.render).toBeTypeOf('function');
    expect(document.head.firstElementChild?.id).toBe('grafana-viz-renderer-css');

    const el = document.createElement('div');
    document.body.append(el);
    const handle = renderer.render(el, readVizFixture('prometheus-line', 'spec.json'));
    const legend = () => el.querySelectorAll('[data-testid^="data-testid VizLegend series"]');
    await vi.waitFor(() => expect(legend().length).toBeGreaterThan(0));

    // The Renderer styles go into the cache of the App's @emotion/css, not into a second one.
    const hashes = [...el.querySelectorAll('[class*="css-"]')].flatMap((node) =>
      [...node.classList].filter((c) => c.startsWith('css-')).map((c) => c.slice(4))
    );
    expect(hashes.length).toBeGreaterThan(0);
    expect(hashes.filter((hash) => !(hash in cache.inserted))).toEqual([]);

    handle.destroy();
    expect(fetch).not.toHaveBeenCalled();
  });
});
