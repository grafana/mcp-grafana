import { test, expect } from '@playwright/test';

test('upstream sandbox isolates host storage and blocks undeclared connections', async ({ page }) => {
  await page.goto('/');
  const app = page.frameLocator('#app').frameLocator('iframe');
  await expect(app.getByTestId('viz-renderer')).toHaveAttribute('data-viz-state', 'ready');
  const result = await app.locator('body').evaluate(async () => {
    const denied = (read: () => unknown) => {
      try { read(); return false; } catch (error) { return error instanceof DOMException && error.name === 'SecurityError'; }
    };
    const violation = new Promise<string>(resolve => {
      document.addEventListener('securitypolicyviolation', event => resolve(event.effectiveDirective), { once: true });
    });
    const blocked = await fetch('https://undeclared.example.invalid/probe').then(() => false, () => true);
    return {
      parentDomDenied: denied(() => window.top!.document),
      hostStorageDenied: denied(() => window.top!.localStorage),
      blocked,
      directive: await violation,
    };
  });
  expect(result).toEqual({ parentDomDenied: true, hostStorageDenied: true, blocked: true, directive: 'connect-src' });
});
