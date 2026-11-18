/**
 * Puts text on the clipboard, and tells whether it did. A host iframe can block the Clipboard API, so this falls back
 * to copying from a hidden text area. Call it from a click, since both ways need a user gesture.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Blocked or missing: try the fallback.
  }
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const area = document.createElement('textarea');
  area.value = text;
  area.readOnly = true;
  area.style.position = 'fixed';
  area.style.opacity = '0';
  document.body.append(area);
  // execCommand copies the selection of the focused element; focus is given back after.
  area.focus();
  area.select();
  try {
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    area.remove();
    focused?.focus();
  }
}
