import * as emotionCss from '@emotion/css';
import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as ReactDOMClient from 'react-dom/client';
import * as jsxRuntime from 'react/jsx-runtime';

/**
 * Global that holds the packages this App shares with code loaded at run time, so that code uses the same React and
 * Emotion instances (#1589). A second React breaks hooks, and a second Emotion cache splits the styles.
 */
export const SHARED_MODULES_GLOBAL = '__grafanaMcpAppModules';

export const sharedModules = Object.freeze({
  react: React,
  'react-dom': ReactDOM,
  'react-dom/client': ReactDOMClient,
  'react/jsx-runtime': jsxRuntime,
  '@emotion/css': emotionCss,
});

/** Puts {@link sharedModules} on the global. Call it before the first render. */
export function exposeSharedModules(target: object = globalThis): void {
  Object.defineProperty(target, SHARED_MODULES_GLOBAL, { value: sharedModules, configurable: true });
}
