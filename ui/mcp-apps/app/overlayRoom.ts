// Space below an overlay, so its shadow is not cut off.
const MARGIN = 8;

/** The bottom of the boxes in el. A box of no height, like the wrapper of a portal, gives the bottom of its children. */
function bottomOf(el: Element, viewportHeight: number): number {
  const rect = el.getBoundingClientRect();
  // A backdrop covers the viewport: it needs no room of its own, and growing for it would never end.
  if (rect.height > 0 && rect.top <= 0 && rect.bottom >= viewportHeight) return 0;
  if (rect.height > 0) return rect.bottom;
  return Math.max(0, ...Array.from(el.children, (child) => bottomOf(child, viewportHeight)));
}

/**
 * Grows the page while an overlay in container is open, like the time picker of the viz Renderer. The overlays are
 * position: fixed, so the page height leaves them out, and the host sizes the frame to the page (autoResize of App).
 * A min-height on body makes the page tall enough, and the frame shrinks again when the overlay closes. Returns a
 * function that stops it.
 */
export function keepRoomForOverlays(container: HTMLElement, body: HTMLElement = document.body): () => void {
  const view = container.ownerDocument.defaultView ?? window;
  let frame = 0;
  const measure = () => {
    frame = 0;
    const bottom = bottomOf(container, view.innerHeight);
    // An overlay that the frame caps (max-height: 100vh) grows on the next pass, when the frame is taller.
    body.style.minHeight = bottom > 0 ? `${Math.ceil(bottom + MARGIN)}px` : '';
  };
  const schedule = () => {
    if (!frame) frame = view.requestAnimationFrame(measure);
  };
  const mutations = new MutationObserver(schedule);
  mutations.observe(container, { childList: true, subtree: true, attributes: true });
  view.addEventListener('resize', schedule);
  return () => {
    mutations.disconnect();
    view.removeEventListener('resize', schedule);
    if (frame) view.cancelAnimationFrame(frame);
    body.style.minHeight = '';
  };
}
