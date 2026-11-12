import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { Bullet, BulletGroup } from '../src/viz/Bullet';

afterEach(cleanup);

describe('Bullet', () => {
  it('exposes the measure against its scale as a meter', () => {
    render(<Bullet value={0.87} unit="percentunit" label="node_memory_utilisation_ratio" />);

    const meter = screen.getByRole('meter');
    expect(meter.getAttribute('aria-valuenow')).toBe('0.87');
    expect(meter.getAttribute('aria-valuemin')).toBe('0');
    // percentunit bounds the scale at 1, not 100.
    expect(meter.getAttribute('aria-valuemax')).toBe('1');
    expect(meter.getAttribute('aria-valuetext')).toBe('87%');
    expect(screen.getByText('87%')).toBeTruthy();
  });

  it('bounds the scale by unit, so percent runs to 100', () => {
    render(<Bullet value={42} unit="percent" />);
    expect(screen.getByRole('meter').getAttribute('aria-valuemax')).toBe('100');
  });

  it('draws a band per threshold step behind the measure', () => {
    const { container } = render(
      <Bullet
        value={91}
        unit="percent"
        thresholds={[
          { value: Number.NEGATIVE_INFINITY, color: '#56c785' },
          { value: 80, color: '#ed861d' },
          { value: 95, color: '#e75065' },
        ]}
      />
    );
    const track = screen.getByRole('meter');
    // Three bands, plus the measure.
    expect(track.children).toHaveLength(4);
    const widths = [...track.children].slice(0, 3).map((band) => (band as HTMLElement).style.width);
    expect(widths).toEqual(['80%', '15%', '5%']);
    expect(container.textContent).toContain('91%');
  });

  it('draws a target marker only when one is supplied', () => {
    const { container: without } = render(<Bullet value={42} unit="percent" />);
    const plain = screen.getByRole('meter').children.length;
    cleanup();
    render(<Bullet value={42} unit="percent" target={60} />);
    expect(screen.getByRole('meter').children.length).toBe(plain + 1);
    expect(without).toBeTruthy();
  });

  it('shows the scale endpoints only when asked, so a group prints them once', () => {
    const { container } = render(
      <BulletGroup>
        <Bullet value={0.42} unit="percentunit" label="a" showScale={false} />
        <Bullet value={0.91} unit="percentunit" label="b" showScale />
      </BulletGroup>
    );
    // "0%" and "100%" appear once between them, not once per bullet.
    expect(container.textContent?.match(/100%/g)).toHaveLength(1);
    expect(screen.getAllByRole('meter')).toHaveLength(2);
  });

  it('clamps a value outside the scale rather than overflowing the track', () => {
    render(<Bullet value={250} unit="percent" label="over" />);
    const measure = screen.getByRole('meter').lastElementChild as HTMLElement;
    expect(measure.style.getPropertyValue('--viz-measure')).toBe('');
    // The custom property lives on the wrapper; the measure reads it.
    const wrapper = screen.getByRole('meter').parentElement as HTMLElement;
    expect(wrapper.style.getPropertyValue('--viz-measure')).toBe('100%');
  });
});
