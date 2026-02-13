// @ts-check
import { test, expect } from '@playwright/test';

test('home page displays hello world message', async ({ page }) => {
	await page.route('/api/hello', async (route) => {
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ message: 'Hello, World!' }),
		});
	});

	await page.goto('/');

	const heading = page.locator('h1');
	await expect(heading).toHaveText('Hello, World!');
});

test('home page shows loading state initially', async ({ page }) => {
	await page.route('/api/hello', async (route) => {
		await new Promise((resolve) => setTimeout(resolve, 2000));
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ message: 'Hello, World!' }),
		});
	});

	await page.goto('/');

	const heading = page.locator('h1');
	await expect(heading).toHaveText('Loading...');
});
