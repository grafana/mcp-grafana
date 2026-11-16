import type { App } from '@modelcontextprotocol/ext-apps';

import { VIZ_RENDERER_GLOBAL } from '../vizRenderer.config';

export type Renderer = typeof import('../renderer/renderer.js');

/** The URI of the Renderer resource, from the App build. */
export const VIZ_RENDERER_URI = __VIZ_RENDERER_URI__;

/** A failed read of the Renderer resource. It may pass on the next try; a script that ran and failed will not. */
class ReadError extends Error {}

let renderer: Promise<Renderer> | undefined;

/**
 * Loads the Renderer once per iframe (#1589). The host reads the Renderer resource from the server, and the App runs
 * its text as an inline script: a blob: import is blocked in VS Code and Cursor, and the sandbox has no network. The
 * script fills the icon cache of its own @grafana/ui (renderer.15). The other views never load it.
 * A failed read is not kept, so the next result reads again. Any other failure is kept.
 */
export function loadRenderer(app: App): Promise<Renderer> {
  renderer ??= readRenderer(app).catch((cause: unknown) => {
    if (cause instanceof ReadError) renderer = undefined;
    throw cause;
  });
  return renderer;
}

async function readRenderer(app: App): Promise<Renderer> {
  if (!app.getHostCapabilities()?.serverResources) {
    throw new Error('This host cannot load the chart, because it does not pass resource reads to the server.');
  }
  let text: string;
  try {
    text = scriptText(await app.readServerResource({ uri: VIZ_RENDERER_URI }));
  } catch (cause) {
    throw new ReadError(`Could not read the chart script: ${messageOf(cause)}`);
  }
  await runInlineScript(text);
  const module = rendererGlobal();
  if (!module) throw new Error('The chart script did not start.');
  return module;
}

const rendererGlobal = () => (window as unknown as Record<string, unknown>)[VIZ_RENDERER_GLOBAL] as Renderer | undefined;

type ReadResult = Awaited<ReturnType<App['readServerResource']>>;

/** The text of the Renderer resource in the read, as text or as a base64 blob. Only that URI is run. */
function scriptText(result: ReadResult): string {
  const content = result.contents.find((c) => c.uri === VIZ_RENDERER_URI);
  if (!content) throw new Error('the host returned another resource');
  const text =
    'text' in content
      ? content.text
      : new TextDecoder().decode(Uint8Array.from(atob(content.blob), (c) => c.charCodeAt(0)));
  if (!text) throw new Error('it came back empty');
  return text;
}

/**
 * Runs the text as an inline script. An error at the top level of the script is rethrown here, and so is a block by
 * the content security policy of the host. That block fires no error event, and its securitypolicyviolation event
 * comes in a later task, so a script that set no global waits one task for it.
 */
async function runInlineScript(text: string): Promise<void> {
  let failure: string | undefined;
  const onError = (event: ErrorEvent) => {
    failure ??= `The chart script failed: ${messageOf(event.error ?? event.message)}`;
  };
  const onBlocked = () => {
    failure ??= 'The content security policy of the host blocked the chart script.';
  };
  window.addEventListener('error', onError);
  document.addEventListener('securitypolicyviolation', onBlocked);
  const script = document.createElement('script');
  script.textContent = text;
  try {
    document.head.appendChild(script);
    if (!failure && !rendererGlobal()) await new Promise((resolve) => setTimeout(resolve, 0));
  } finally {
    script.remove();
    window.removeEventListener('error', onError);
    document.removeEventListener('securitypolicyviolation', onBlocked);
  }
  if (failure) throw new Error(failure);
}

/** The message of an error without its trailing period. Not instanceof Error: the error can come from another realm. */
function messageOf(cause: unknown): string {
  const message = (cause as { message?: unknown } | null)?.message;
  return (typeof message === 'string' ? message : String(cause)).replace(/\.$/, '');
}
