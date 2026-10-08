import { describe, expect, it } from 'vitest';

import { escapeHTML, isSafeUrl } from '../src/sanitize';

describe('escapeHTML', () => {
  it('escapes markup so it renders as text', () => {
    expect(escapeHTML('<img src=x onerror="alert(1)">')).toBe('&#60;img src=x onerror=&#34;alert(1)&#34;&#62;');
    expect(escapeHTML(`a & b's`)).toBe('a &#38; b&#39;s');
    expect(escapeHTML('plain')).toBe('plain');
  });
});

describe('isSafeUrl', () => {
  it('accepts plain http(s) links', () => {
    expect(isSafeUrl('https://example.grafana.net/explore?panes=%7B%7D')).toBe(true);
    expect(isSafeUrl('http://localhost:3000/d/abc')).toBe(true);
  });

  it('rejects executable schemes, credentials, and non-strings', () => {
    expect(isSafeUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeUrl('data:text/html,<script>alert(1)</script>')).toBe(false);
    expect(isSafeUrl('https://user:pass@example.com/')).toBe(false);
    expect(isSafeUrl('')).toBe(false);
    expect(isSafeUrl(undefined)).toBe(false);
    expect(isSafeUrl(42)).toBe(false);
  });

  it('rejects relative paths unless allowed', () => {
    expect(isSafeUrl('/d/abc')).toBe(false);
    expect(isSafeUrl('/d/abc', { allowRelative: true })).toBe(true);
    expect(isSafeUrl('javascript:alert(1)', { allowRelative: true })).toBe(false);
    // Protocol-relative URLs resolve to another host, so credentials still count.
    expect(isSafeUrl('//user:pass@evil.example/', { allowRelative: true })).toBe(false);
  });
});
