import * as emotionCss from '@emotion/css';
import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as ReactDOMClient from 'react-dom/client';
import * as jsxRuntime from 'react/jsx-runtime';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { packageOf, SHARED_MODULE_IDS, sharedPackageCopies } from '../sharedModules.config';
import { exposeSharedModules, SHARED_MODULES_GLOBAL, sharedModules } from '../app/sharedModules';

describe('sharedModules', () => {
  it('exposes the instances the App itself imports', () => {
    const target = {};
    exposeSharedModules(target);
    const exposed = (target as Record<string, typeof sharedModules>)[SHARED_MODULES_GLOBAL];

    expect(exposed.react).toBe(React);
    expect(exposed['react-dom']).toBe(ReactDOM);
    expect(exposed['react-dom/client']).toBe(ReactDOMClient);
    expect(exposed['react/jsx-runtime']).toBe(jsxRuntime);
    expect(exposed['@emotion/css']).toBe(emotionCss);
    expect(Object.isFrozen(exposed)).toBe(true);
  });

  it('exposes each package of the list, in order', () => {
    expect(Object.keys(sharedModules)).toEqual([...SHARED_MODULE_IDS]);
  });

  it('finds a bundled copy of a shared package at any path', () => {
    expect(
      sharedPackageCopies([
        '/x/node_modules/react-dom/cjs/react-dom-server.production.js',
        '/x/node_modules/@grafana/ui/node_modules/@emotion/css/dist/emotion-css.esm.js',
        '/x/node_modules/@emotion/react/dist/emotion-react.esm.js',
        '/x/node_modules/lodash.debounce/index.js',
        '/x/app/vizRenderer.entry.ts',
      ])
    ).toEqual([
      '/x/node_modules/react-dom/cjs/react-dom-server.production.js',
      '/x/node_modules/@grafana/ui/node_modules/@emotion/css/dist/emotion-css.esm.js',
    ]);
    expect(packageOf('/x/node_modules/lodash.debounce/index.js')).toBe('lodash.debounce');
  });

  it('reads only listed packages from the global in the built Renderer resource', () => {
    const source = readFileSync(join(__dirname, '..', 'dist', 'viz-renderer.js'), 'utf8');
    const read = new Set([...source.matchAll(/__grafanaMcpAppModules(?:\.(\w+)|\[`([^`]+)`\])/g)].map((m) => m[1] ?? m[2]));

    expect([...read].sort()).toEqual([...SHARED_MODULE_IDS].sort());
  });
});
