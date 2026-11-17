import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { keepRoomForOverlays } from '../app/overlayRoom';

// jsdom has no layout: each test element gives the rect of its data-rect attribute, "top bottom".
function rectOf(this: Element): DOMRect {
  const [top = 0, bottom = 0] = (this.getAttribute('data-rect') ?? '0 0').split(' ').map(Number);
  return { top, bottom, height: bottom - top, left: 0, right: 0, width: 0, x: 0, y: top, toJSON: () => ({}) };
}

function overlay(rect: string): HTMLElement {
  // A portal wrapper of no height, as Portal of @grafana/ui draws it, holding the overlay box.
  const wrapper = document.createElement('div');
  const box = document.createElement('section');
  box.setAttribute('data-rect', rect);
  wrapper.append(box);
  return wrapper;
}

const nextFrame = () => vi.advanceTimersToNextFrame();

describe('keepRoomForOverlays', () => {
  let container: HTMLElement;
  let stop: () => void;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(rectOf);
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(500);
    container = document.createElement('div');
    document.body.append(container);
    stop = keepRoomForOverlays(container);
  });

  afterEach(() => {
    stop();
    container.remove();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('grows the page to the bottom of an open overlay, and shrinks it when the overlay closes', async () => {
    const open = overlay('200 620');
    container.append(open);
    await Promise.resolve();
    nextFrame();
    expect(document.body.style.minHeight).toBe('628px');

    open.remove();
    await Promise.resolve();
    nextFrame();
    expect(document.body.style.minHeight).toBe('');
  });

  it('gives a backdrop that covers the viewport no room', async () => {
    container.append(overlay('0 500'));
    await Promise.resolve();
    nextFrame();
    expect(document.body.style.minHeight).toBe('');
  });

  it('measures again when the frame changes size', () => {
    container.append(overlay('100 300'));
    window.dispatchEvent(new Event('resize'));
    nextFrame();
    expect(document.body.style.minHeight).toBe('308px');
  });

  it('clears the min-height when it stops', async () => {
    container.append(overlay('100 300'));
    await Promise.resolve();
    nextFrame();
    stop();
    expect(document.body.style.minHeight).toBe('');
  });
});
