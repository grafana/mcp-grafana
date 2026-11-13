import * as emotionCss from '@emotion/css';
import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as ReactDOMClient from 'react-dom/client';
import * as jsxRuntime from 'react/jsx-runtime';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
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

  it('covers every Renderer import except the packages the Renderer resource will bundle', () => {
    const source = readFileSync(join(__dirname, '..', 'renderer', 'renderer.js'), 'utf8');
    // Bare package specifiers only; the minified code also has strings that follow the word "from".
    const imports = new Set([...source.matchAll(/\bfrom\s*["']((?:@[\w-]+\/)?[\w-]+(?:\/[\w-]+)*)["']/g)].map((m) => m[1]));
    const bundledByRenderer = new Set(['@grafana/ui', '@grafana/data']);

    expect(imports.size).toBeGreaterThan(bundledByRenderer.size);
    expect([...imports].filter((id) => !bundledByRenderer.has(id) && !(id in sharedModules))).toEqual([]);
  });
});
