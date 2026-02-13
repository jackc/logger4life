// @ts-check
import { test, expect } from '@playwright/test';

function uniqueUsername() {
	return 'test_' + Date.now() + '_' + Math.random().toString(36).slice(2, 8);
}

const testPassword = 'password123';

async function registerAndLogin(page) {
	const username = uniqueUsername();
	await page.goto('/register');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');
	await page.waitForURL('/logs');
	return username;
}

test('home page shows landing page when unauthenticated', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Logger4Life' })).toBeVisible();
	await expect(page.locator('main a[href="/login"]')).toBeVisible();
	await expect(page.locator('main a[href="/register"]')).toBeVisible();
});

test('home page shows quick log when authenticated', async ({ page }) => {
	await registerAndLogin(page);
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Quick Log' })).toBeVisible();
});

test('home page shows empty state with link to create logs', async ({ page }) => {
	await registerAndLogin(page);
	await page.goto('/');
	await expect(page.getByText("You don't have any logs yet")).toBeVisible();
	await expect(page.locator('main a[href="/logs"]')).toBeVisible();
});

test('home page shows log cards for existing logs', async ({ page }) => {
	await registerAndLogin(page);

	// Create a log via /logs page
	await page.fill('input[name="log-name"]', 'Water');
	await page.click('button:has-text("Create Log")');
	await expect(page.getByRole('link', { name: 'Water' })).toBeVisible();

	// Go home
	await page.goto('/');
	await expect(page.locator('[data-testid="log-card"]')).toHaveCount(1);
	await expect(page.getByText('Water')).toBeVisible();
});

test('quick log entry for log without fields', async ({ page }) => {
	await registerAndLogin(page);

	// Create a log
	await page.fill('input[name="log-name"]', 'Water');
	await page.click('button:has-text("Create Log")');

	await page.goto('/');
	await page.click('[data-testid="log-card"] button:has-text("Log It")');
	await expect(page.getByText('Logged!')).toBeVisible();
});

test('quick log entry for log with fields', async ({ page }) => {
	await registerAndLogin(page);

	// Create log with a required number field
	await page.fill('input[name="log-name"]', 'Pushups');
	await page.click('button:has-text("Add Field")');
	await page.fill('input[placeholder="Field name"]', 'count');
	await page.check('input[type="checkbox"]');
	await page.click('button:has-text("Create Log")');

	await page.goto('/');
	const card = page.locator('[data-testid="log-card"]');
	await expect(card.locator('input[type="number"]')).toBeVisible();

	await card.locator('input[type="number"]').fill('25');
	await card.locator('button:has-text("Log It")').click();
	await expect(card.getByText('Logged!')).toBeVisible();
});

test('view entries link navigates to log detail', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Water');
	await page.click('button:has-text("Create Log")');

	await page.goto('/');
	await page.click('a:has-text("View entries")');
	await expect(page.locator('h1:has-text("Water")')).toBeVisible();
});
