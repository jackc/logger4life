// @ts-check
import { test, expect } from '@playwright/test';

function uniqueUsername() {
	return 'test_' + Date.now() + '_' + Math.random().toString(36).slice(2, 8);
}

const testPassword = 'password123';

test('register page shows form', async ({ page }) => {
	await page.goto('/register');

	await expect(page.locator('input[name="username"]')).toBeVisible();
	await expect(page.locator('input[name="email"]')).toBeVisible();
	await expect(page.locator('input[name="password"]')).toBeVisible();
	await expect(page.locator('button[type="submit"]')).toBeVisible();
});

test('login page shows form', async ({ page }) => {
	await page.goto('/login');

	await expect(page.locator('input[name="username"]')).toBeVisible();
	await expect(page.locator('input[name="password"]')).toBeVisible();
	await expect(page.locator('button[type="submit"]')).toBeVisible();
});

test('register a new user and land on logs page', async ({ page }) => {
	const username = uniqueUsername();

	await page.goto('/register');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="email"]', `${username}@example.com`);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');

	await page.waitForURL('/logs');
	await expect(page.getByRole('heading', { name: 'My Logs' })).toBeVisible();
});

test('register with duplicate username shows error', async ({ page, request }) => {
	const username = uniqueUsername();

	// Register the user via API first
	await request.post('/api/register', {
		data: { username, password: testPassword },
	});

	await page.goto('/register');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');

	await expect(page.locator('text=username already taken')).toBeVisible();
});

test('login with valid credentials', async ({ page, request }) => {
	const username = uniqueUsername();

	await request.post('/api/register', {
		data: { username, password: testPassword },
	});

	await page.goto('/login');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');

	await page.waitForURL('/logs');
	await expect(page.getByRole('heading', { name: 'My Logs' })).toBeVisible();
});

test('login with wrong password shows error', async ({ page, request }) => {
	const username = uniqueUsername();

	await request.post('/api/register', {
		data: { username, password: testPassword },
	});

	await page.goto('/login');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', 'wrongpassword');
	await page.click('button[type="submit"]');

	await expect(page.locator('text=invalid username or password')).toBeVisible();
});

test('logout clears session', async ({ page }) => {
	const username = uniqueUsername();

	// Register (auto-login)
	await page.goto('/register');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');
	await page.waitForURL('/logs');

	// Logout
	await page.click('button:has-text("Logout")');

	// Should no longer be able to access /me
	await page.goto('/me');
	await page.waitForURL('/login');
});

test('nav shows login/register links when unauthenticated', async ({ page }) => {
	await page.goto('/');

	await expect(page.locator('nav a[href="/login"]')).toBeVisible();
	await expect(page.locator('nav a[href="/register"]')).toBeVisible();
});

test('nav shows username and logout when authenticated', async ({ page }) => {
	const username = uniqueUsername();

	await page.goto('/register');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');
	await page.waitForURL('/logs');

	await page.goto('/');

	await expect(page.getByText(username)).toBeVisible();
	await expect(page.locator('button:has-text("Logout")')).toBeVisible();
	await expect(page.locator('a[href="/login"]')).not.toBeVisible();
});

test('/me redirects to login when unauthenticated', async ({ page }) => {
	await page.goto('/me');

	await page.waitForURL('/login');
});
