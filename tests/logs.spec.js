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

test('create a log with custom fields', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Pushups');
	await page.click('button:has-text("Add Field")');
	await page.fill('input[placeholder="Field name"]', 'count');
	// Type defaults to "Number", check required
	await page.check('input[type="checkbox"]');

	await page.click('button:has-text("Create Log")');
	await expect(page.getByRole('link', { name: 'Pushups' })).toBeVisible();
});

test('create entry with field values', async ({ page }) => {
	await registerAndLogin(page);

	// Create log with a required number field
	await page.fill('input[name="log-name"]', 'Pushups');
	await page.click('button:has-text("Add Field")');
	await page.fill('input[placeholder="Field name"]', 'count');
	await page.check('input[type="checkbox"]');
	await page.click('button:has-text("Create Log")');
	await page.click('a:has-text("Pushups")');

	await expect(page.locator('h1:has-text("Pushups")')).toBeVisible();

	// Fill in the count field and log it
	await page.fill('input[name="field-count"]', '25');
	await page.click('button:has-text("Log It")');

	// Verify the entry shows with the field value
	const entry = page.locator('[data-testid="log-entry"]').first();
	await expect(entry).toBeVisible();
	await expect(entry).toContainText('count');
	await expect(entry).toContainText('25');
});

test('log without fields works with simple button', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Water');
	await page.click('button:has-text("Create Log")');
	await page.click('a:has-text("Water")');

	// No field inputs should be shown, just the button
	await expect(page.locator('input[name^="field-"]')).toHaveCount(0);

	await page.click('button:has-text("Log It")');
	await expect(page.locator('[data-testid="log-entry"]').first()).toBeVisible();
});

test('create log with multiple fields', async ({ page }) => {
	await registerAndLogin(page);

	await page.fill('input[name="log-name"]', 'Exercise');

	// Add first field: reps (number, required)
	await page.click('button:has-text("Add Field")');
	await page.locator('input[placeholder="Field name"]').first().fill('reps');
	await page.locator('input[type="checkbox"]').first().check();

	// Add second field: notes (text, optional)
	await page.click('button:has-text("Add Field")');
	await page.locator('input[placeholder="Field name"]').nth(1).fill('notes');
	await page.locator('select').nth(1).selectOption('text');

	await page.click('button:has-text("Create Log")');
	await page.click('a:has-text("Exercise")');

	// Both field inputs should appear
	await expect(page.locator('input[name="field-reps"]')).toBeVisible();
	await expect(page.locator('input[name="field-notes"]')).toBeVisible();

	// Required field shows asterisk
	await expect(page.getByText('reps*')).toBeVisible();

	// Fill in values and log
	await page.fill('input[name="field-reps"]', '15');
	await page.fill('input[name="field-notes"]', 'morning set');
	await page.click('button:has-text("Log It")');

	const entry = page.locator('[data-testid="log-entry"]').first();
	await expect(entry).toContainText('15');
	await expect(entry).toContainText('morning set');
});
