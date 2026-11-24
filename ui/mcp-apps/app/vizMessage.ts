import constants from '@grafana/viz-contract/registry/constants.json';
import registry from '@grafana/viz-contract/registry/messages.json';

import type { MessageRequest } from '../renderer/renderer.js';
import type { VizSpec } from './vizSpec';

const MAX_CHARS = constants.maxMessageChars.value;
const MAX_LABELS = constants.maxMessageLabels.value;
const MAX_VALUE_CHARS = constants.maxMessageValueChars.value;
const PATTERNS = new Map(registry.messages.map((m) => [m.code, m.message]));

// state.23 step 1: the characters that do not show and the control characters, but not the joiners of step 2.
const HIDDEN = new RegExp(
  '(?![\\u200C\\u200D])[' +
    '\\p{Cc}\\p{Cf}\\p{Zl}\\p{Zp}' + // control, format, line and paragraph separator characters
    '\\uFE00-\\uFE0F\\u{E0100}-\\u{E01EF}' + // variation selectors
    '\\u115F\\u1160\\u2800\\u3164\\uFFA0' + // blank letters
    ']',
  'gu',
);
// renderer.32
const ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

const isJoiner = (char: string) => char === '\u200C' || char === '\u200D';

/** state.23 steps 1 to 3: a value of the data source with no hidden characters, and cut to maxMessageValueChars. */
function clean(value: string): string {
  const chars = [...value.replace(HIDDEN, '')];
  // Step 2: a joiner stays only between two characters that are not joiners. A missing neighbor counts as a joiner.
  const kept = chars.filter(
    (char, i) => !isJoiner(char) || !(isJoiner(chars[i - 1] ?? '\u200D') || isJoiner(chars[i + 1] ?? '\u200D')),
  );
  return kept.length > MAX_VALUE_CHARS ? kept.slice(0, MAX_VALUE_CHARS - 1).join('') + '…' : kept.join('');
}

/** state.23 step 4. */
const quote = (value: string) => JSON.stringify(clean(value));

/**
 * The text of the message for a point (state.22 to state.25), or undefined when even the text with no labels is longer
 * than maxMessageChars.
 */
export function vizMessageText(spec: VizSpec, { series, point }: MessageRequest): string | undefined {
  const time = point.xType === 'time' && !!point.xIso && ISO.test(point.xIso);
  const pattern = PATTERNS.get(time ? 'point-time' : point.xType === 'number' ? 'point-number' : 'point-string')!;

  // The first label of each key after cleaning, then the first maxMessageLabels of them.
  const labels = new Map<string, string>();
  for (const [key, value] of Object.entries(series.labels)) {
    const k = clean(key);
    if (!labels.has(k)) labels.set(k, clean(value));
  }
  const pairs = [...labels].slice(0, MAX_LABELS).map(([k, v]) => `${JSON.stringify(k)}:${JSON.stringify(v)}`);

  const values: Record<string, string> = {
    series: quote(series.name),
    refId: series.refId === undefined ? 'null' : quote(series.refId),
    x: quote(point.x),
    y: quote(point.y),
    xIso: time ? point.xIso! : '',
    chart: quote(spec.panel.title || spec.panel.type),
    from: new Date(spec.data.range.from).toISOString(),
    to: new Date(spec.data.range.to).toISOString(),
  };
  // One pass, so a placeholder in a value stays as it is.
  const fill = (count: number) =>
    pattern.replace(/<(\w+)>/g, (match, name: string) =>
      name === 'labels' ? `{${pairs.slice(0, count).join(',')}}` : (values[name] ?? match),
    );
  // state.25: remove labels from the end until the text fits. Lengths are in code points.
  for (let count = pairs.length; count >= 0; count--) {
    const text = fill(count);
    if ([...text].length <= MAX_CHARS) return text;
  }
  return undefined;
}
