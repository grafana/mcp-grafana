/**
 * Escaping and URL checks for MCP app UIs.
 *
 * React escapes everything rendered through JSX, so these are only for the
 * places that bypass it: strings handed to a library as HTML, and URLs that
 * become an `href`. Kept small and dependency-free rather than pulling in
 * `@grafana/data`'s `textUtil`, since an app ships as one self-contained resource.
 */

/** Escape text for insertion into an HTML string, e.g. an ECharts tooltip formatter. */
export function escapeHTML(text: string): string {
  return text.replace(/[&<>"']/g, (ch) => `&#${ch.charCodeAt(0)};`);
}

/**
 * True for an http(s) URL carrying no credentials — anything else, such as a
 * `javascript:` or `data:` URL, must not become a link.
 *
 * With `allowRelative`, paths resolve against a placeholder origin first, so a
 * relative Grafana path passes while a protocol-relative `//host` URL is still
 * checked for credentials.
 */
export function isSafeUrl(value: unknown, { allowRelative = false }: { allowRelative?: boolean } = {}): value is string {
  if (typeof value !== 'string' || !value.trim()) return false;
  try {
    const url = allowRelative ? new URL(value, 'https://grafana.invalid') : new URL(value);
    return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password;
  } catch {
    return false;
  }
}
