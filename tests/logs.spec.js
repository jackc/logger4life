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

test('logs page requires authentication', async ({ page }) => {
	await page.goto('/logs');
	await page.waitForURL('/login');
});

test('logs page shows empty state', async ({ page }) => {
	await registerAndLogin(page);

	await expect(page.locator('h1:has-text("My Logs")')).toBeVisible();
	await expect(page.getByText('No logs yet')).toBeVisible();
});

test('create a new log', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Vitamins');
	await page.click('button:has-text("Create Log")');

	await expect(page.getByRole('link', { name: 'Vitamins' })).toBeVisible();
});

test('create log with duplicate name shows error', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Vitamins');
	await page.click('button:has-text("Create Log")');
	await expect(page.getByRole('link', { name: 'Vitamins' })).toBeVisible();

	await page.fill('input[name="log-name"]', 'Vitamins');
	await page.click('button:has-text("Create Log")');
	await expect(page.getByText('already exists')).toBeVisible();
});

test('navigate to log detail and create entry', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Pushups');
	await page.click('button:has-text("Create Log")');
	await page.click('a:has-text("Pushups")');

	await expect(page.locator('h1:has-text("Pushups")')).toBeVisible();

	await page.click('button:has-text("Log It")');

	await expect(page.locator('[data-testid="log-entry"]').first()).toBeVisible();
});

test('multiple entries appear in list', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Water');
	await page.click('button:has-text("Create Log")');
	await page.click('a:has-text("Water")');

	await page.click('button:has-text("Log It")');
	await expect(page.locator('[data-testid="log-entry"]')).toHaveCount(1);

	await page.click('button:has-text("Log It")');
	await expect(page.locator('[data-testid="log-entry"]')).toHaveCount(2);
});

test('log detail page requires authentication', async ({ page }) => {
	await page.goto('/logs/00000000-0000-0000-0000-000000000000');
	await page.waitForURL('/login');
});
