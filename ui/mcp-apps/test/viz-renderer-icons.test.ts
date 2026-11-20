import { fireEvent } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterAll, describe, expect, it, vi } from 'vitest';

import { exposeSharedModules, SHARED_MODULES_GLOBAL } from '../app/sharedModules';
import icons from '../renderer/icons.json';
import { readVizFixture, vizSpecFixtureSets } from './vizFixtures';

type Renderer = typeof import('../renderer/renderer.js');

// uPlot, which @grafana/ui loads, reads matchMedia at import; jsdom has none.
window.matchMedia ??= (() => ({ matches: false, addEventListener() {}, removeEventListener() {} })) as never;

// Icons of icons.json that only a state with layout or a canvas shows: the close button and the Ask in chat link of a
// pinned tooltip.
const NOT_IN_JSDOM = ['unicons/times', 'unicons/link'];

// Icons that the Renderer shows but its icons.json lacks, so Icon requests them. The open time picker with no recent
// ranges has an external "Read the documentation" link (grafana/dataViz#234). Remove an entry when a Renderer release
// adds the icon.
const MISSING_FROM_ICONS_JSON = ['unicons/external-link-alt'];

const global = globalThis as Record<string, unknown>;

afterAll(() => {
  delete global.__grafanaVizRenderer;
  delete global[SHARED_MODULES_GLOBAL];
});

/** The icons in the document that have their SVG. Icon of @grafana/ui draws it only from the cache. */
const drawnIcons = () =>
  [...document.querySelectorAll('[data-testid^="icon-"]')]
    .filter((icon) => icon.querySelector('path'))
    .map((icon) => icon.getAttribute('data-testid')!.slice('icon-'.length));

/** Waits until the drawn icons stop changing, then returns them. */
async function settledIcons() {
  let last: string | undefined;
  return vi.waitFor(
    () => {
      const now = drawnIcons();
      const same = now.join() === last;
      last = now.join();
      expect(same).toBe(true);
      return now;
    },
    { interval: 25 }
  );
}

// delivery.7, in the states of delivery.8 that jsdom can show: the built Renderer resource fills its icon cache, then
// draws each fixture with and with no onRefresh, and opens the time picker. The narrow toolbar and the tooltip states
// need layout or a canvas, so they are left to the Renderer CI (delivery.8).
describe('dist/viz-renderer.js', () => {
  it('draws each icon of icons.json from its cache, with no other request', { timeout: 60_000 }, async () => {
    // jsdom has no canvas; ECharts then draws nothing, without an error for each chart.
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const requests: string[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      requests.push(input instanceof Request ? input.url : String(input));
      throw new Error('no network in the sandbox');
    });
    exposeSharedModules(globalThis);
    (0, eval)(readFileSync(join(__dirname, '..', 'dist', 'viz-renderer.js'), 'utf8'));
    const renderer = global.__grafanaVizRenderer as Renderer;

    const drawn = new Set<string>();
    const draw = async (set: string, refresh: boolean, openTimePicker = false) => {
      const el = document.createElement('div');
      document.body.append(el);
      const handle = renderer.render(el, readVizFixture(set, 'spec.json'), refresh ? { onRefresh() {} } : {});
      await vi.waitFor(() => expect(el.childElementCount).toBeGreaterThan(0));
      if (refresh) await vi.waitFor(() => expect(el.querySelector('[aria-label="Refresh"]')).not.toBeNull());
      if (openTimePicker) {
        fireEvent.click(el.querySelector('[data-testid="data-testid TimePicker Open Button"]')!);
        await vi.waitFor(() => expect(document.getElementById('TimePickerContent')).not.toBeNull());
      }
      for (const icon of await settledIcons()) drawn.add(icon);
      handle.destroy();
      el.remove();
    };

    for (const set of vizSpecFixtureSets) {
      await draw(set, false);
      await draw(set, true);
    }
    await draw('prometheus-line', true, true);

    const names = (keys: string[]) => keys.map((key) => key.slice(key.indexOf('/') + 1)).sort();
    expect([...drawn].sort()).toEqual(names(Object.keys(icons).filter((key) => !NOT_IN_JSDOM.includes(key))));
    expect(requests).toEqual(MISSING_FROM_ICONS_JSON.map((key) => `public/build/img/icons/${key}.svg`));
  });
});
