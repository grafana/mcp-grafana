import { Icon } from '@grafana/ui';
import { render } from '@testing-library/react';
import { cacheStore } from 'react-inlinesvg';
import { describe, expect, it, vi } from 'vitest';

import { fillIconCache } from '../app/iconCache';
import icons from '../renderer/icons.json';
import { iconCacheGuard, packageCopies } from '../sharedModules.config';

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

  // delivery.10: iconCacheGuard fails the App and Renderer builds on a second copy.
  it('counts each installed copy and each module format of a package', () => {
    const root = '/x/node_modules/react-inlinesvg/dist';
    const one = [`${root}/index.mjs`, `${root}/provider-a.mjs`, '/x/app/main.tsx'];
    expect(packageCopies(one, 'react-inlinesvg')).toEqual(['/x/node_modules/react-inlinesvg']);
    expect(
      packageCopies(
        [
          `\0${root}/index.cjs?commonjs-module`,
          `${root}/index.mjs`,
          '/x/node_modules/@grafana/ui/node_modules/react-inlinesvg/dist/index.mjs',
        ],
        'react-inlinesvg'
      )
    ).toEqual([
      '/x/node_modules/@grafana/ui/node_modules/react-inlinesvg',
      '/x/node_modules/react-inlinesvg',
      '/x/node_modules/react-inlinesvg (CommonJS)',
    ]);
  });

  it('fails the build on a second copy', () => {
    const error = vi.fn();
    const generate = iconCacheGuard().generateBundle as (this: unknown, options: unknown, bundle: unknown) => void;
    const chunk = (...moduleIds: string[]) => ({ type: 'chunk', moduleIds });
    const one = '/x/node_modules/react-inlinesvg/dist/index.mjs';

    generate.call({ error }, {}, { 'a.js': chunk(one), 'b.js': chunk(one) });
    expect(error).not.toHaveBeenCalled();

    const nested = '/x/node_modules/y/node_modules/react-inlinesvg/dist/index.mjs';
    generate.call({ error }, {}, { 'a.js': chunk(one), 'b.js': chunk(nested) });
    expect(error).toHaveBeenCalledWith(expect.stringContaining('2 copies of react-inlinesvg'));
  });
});
