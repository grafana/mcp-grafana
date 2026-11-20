import { describe, expect, it } from 'vitest';

import type { MessageRequest } from '../renderer/renderer.js';
import { vizMessageText } from '../app/vizMessage';
import { readVizFixture, vizMessageFixtureSets } from './vizFixtures';

type MessageCase = { description: string; request: MessageRequest; text: string | null };

// fixtures.15: the text of each case of message.json, or no message when text is null.
describe.each(vizMessageFixtureSets)('vizMessageText: %s', (set) => {
  const spec = readVizFixture(set, 'spec.json');
  const cases: MessageCase[] = readVizFixture(set, 'message.json').cases;
  it.each(cases)('$description', ({ request, text }) => {
    expect(vizMessageText(spec, request) ?? null).toBe(text);
  });
});
