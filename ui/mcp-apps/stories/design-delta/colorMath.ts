/** Small color helpers for the token comparison: parse a CSS color, then measure the distance in OKLab. */

export type RGBA = { r: number; g: number; b: number; a: number };
export type Verdict = 'match' | 'close' | 'different' | 'missing';

/** Parses #rgb, #rgba, #rrggbb, #rrggbbaa, rgb() and rgba(). Anything else (hsl(from …), color-mix) is undefined. */
export function parseColor(input: string | undefined): RGBA | undefined {
  if (!input) return undefined;
  const s = input.trim().toLowerCase();
  const hex = /^#([0-9a-f]{3,8})$/.exec(s);
  if (hex) {
    let h = hex[1];
    if (h.length === 3 || h.length === 4) h = [...h].map((c) => c + c).join('');
    if (h.length !== 6 && h.length !== 8) return undefined;
    const n = (i: number) => parseInt(h.slice(i, i + 2), 16);
    return { r: n(0), g: n(2), b: n(4), a: h.length === 8 ? n(6) / 255 : 1 };
  }
  const fn = /^rgba?\(([^)]+)\)$/.exec(s);
  if (fn) {
    const parts = fn[1].split(/[\s,/]+/).filter(Boolean);
    if (parts.length < 3) return undefined;
    const channel = (p: string) => (p.endsWith('%') ? (parseFloat(p) / 100) * 255 : parseFloat(p));
    const alpha = parts[3] === undefined ? 1 : parts[3].endsWith('%') ? parseFloat(parts[3]) / 100 : parseFloat(parts[3]);
    const [r, g, b] = [channel(parts[0]), channel(parts[1]), channel(parts[2])];
    if ([r, g, b, alpha].some(Number.isNaN)) return undefined;
    return { r, g, b, a: alpha };
  }
  return undefined;
}

/** Composites a translucent color over an opaque base, as the browser would paint it. */
export const over = (c: RGBA, base: RGBA): RGBA => ({
  r: c.r * c.a + base.r * (1 - c.a),
  g: c.g * c.a + base.g * (1 - c.a),
  b: c.b * c.a + base.b * (1 - c.a),
  a: 1,
});

const linear = (v: number) => {
  const c = v / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
};

function oklab({ r, g, b }: RGBA): [number, number, number] {
  const [lr, lg, lb] = [linear(r), linear(g), linear(b)];
  const l = Math.cbrt(0.4122214708 * lr + 0.5363325363 * lg + 0.0514459929 * lb);
  const m = Math.cbrt(0.2119034982 * lr + 0.6806995451 * lg + 0.1073969566 * lb);
  const s = Math.cbrt(0.0883024619 * lr + 0.2817188376 * lg + 0.6299787005 * lb);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
}

/** Euclidean distance in OKLab. About 0.02 is the smallest difference most people can see. */
export function deltaE(a: RGBA, b: RGBA): number {
  const [x, y] = [oklab(a), oklab(b)];
  return Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2]);
}

/** The thresholds of the verdict, shown in the legend of the Tokens story. */
export const MATCH_MAX = 0.015;
export const CLOSE_MAX = 0.03;

export function classifyColor(
  freya: string,
  code: string | undefined,
  base: string,
): { verdict: Verdict; distance?: number } {
  if (!code) return { verdict: 'missing' };
  const a = parseColor(freya);
  const b = parseColor(code);
  if (!a || !b) return { verdict: freya.trim() === code.trim() ? 'match' : 'different' };
  const bg = parseColor(base) ?? { r: 255, g: 255, b: 255, a: 1 };
  const distance = deltaE(over(a, bg), over(b, bg));
  return { verdict: distance <= MATCH_MAX ? 'match' : distance <= CLOSE_MAX ? 'close' : 'different', distance };
}
