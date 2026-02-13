// @ts-check
import { test, expect } from '@playwright/test';

test('home page displays hello world message', async ({ page }) => {
	await page.goto('/');

	const heading = page.locator('h1');
	await expect(heading).toHaveText('Hello, World!');
});
