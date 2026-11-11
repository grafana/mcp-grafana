import { Icon } from '@grafana/ui';
import { render } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cacheStore } from 'react-inlinesvg';
import { describe, expect, it, vi } from 'vitest';

import { fillIconCache } from '../app/loadRenderer';
import icons from '../renderer/icons.json';

// uPlot, which @grafana/ui loads, reads matchMedia at import; jsdom has none.
vi.hoisted(() => {
  window.matchMedia ??= (() => ({ matches: false, addEventListener() {}, removeEventListener() {} })) as never;
});

describe('icon cache', () => {
  it('draws each icon of icons.json with no request (renderer.15, renderer.16)', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch');
    fillIconCache(cacheStore, icons);
    for (const key of Object.keys(icons)) {
      const name = key.slice(key.indexOf('/') + 1);
      const { container, unmount } = render(<Icon name={name as never} />);
      await vi.waitFor(() => expect(container.querySelector('svg path'), key).not.toBeNull());
      unmount();
    }
    expect(fetch).not.toHaveBeenCalled();
  });

  // delivery.10: with two copies, the App fills a cache that Icon does not read.
  it('has one copy of react-inlinesvg', () => {
    const lock = JSON.parse(readFileSync(join(__dirname, '..', 'package-lock.json'), 'utf8'));
    const copies = Object.keys(lock.packages).filter((path) => path.endsWith('node_modules/react-inlinesvg'));
    expect(copies).toEqual(['node_modules/react-inlinesvg']);
  });
});
