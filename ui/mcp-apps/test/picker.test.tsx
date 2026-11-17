import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { Picker } from '../src/Picker';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const options = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
];

/** The side that the open menu is pinned to, for a trigger whose right edge is at `right`. */
function menuSide(right: number) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ right } as DOMRect);
  render(<Picker label="Letters" options={options} value="a" onChange={() => {}} />);
  fireEvent.click(screen.getByRole('button', { name: 'Alpha' }));
  const style = getComputedStyle(screen.getByRole('listbox', { name: 'Letters' }));
  const side = { left: style.left, right: style.right };
  cleanup();
  vi.restoreAllMocks();
  return side;
}

describe('Picker', () => {
  it('picks an option and closes', () => {
    const onChange = vi.fn();
    render(<Picker label="Letters" options={options} value="a" onChange={onChange} triggerLabel="Letter: Alpha" />);
    fireEvent.click(screen.getByRole('button', { name: 'Letter: Alpha' }));
    fireEvent.click(screen.getByRole('option', { name: 'Beta' }));
    expect(onChange).toHaveBeenCalledWith('b');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('closes on Escape and on a click outside', () => {
    render(<Picker label="Letters" options={options} value="a" onChange={() => {}} />);
    const trigger = screen.getByRole('button', { name: 'Alpha' });
    fireEvent.click(trigger);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
    fireEvent.click(trigger);
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('opens the menu to the right of a trigger with no room on its left', () => {
    expect(menuSide(600)).toEqual({ left: 'auto', right: '0px' });
    expect(menuSide(100)).toEqual({ left: '0px', right: 'auto' });
  });
});
