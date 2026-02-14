// @ts-check
import { test, expect } from '@playwright/test';

function uniqueUsername() {
	return 'test_' + Date.now() + '_' + Math.random().toString(36).slice(2, 8);
}

const testPassword = 'password123';

/**
 * Register a user via API and log them in via the browser.
 * Returns the username.
 */
async function registerAndLogin(page, request, { email } = {}) {
	const username = uniqueUsername();
	await request.post('/api/register', {
		data: { username, email, password: testPassword },
	});

	await page.goto('/login');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');
	await page.waitForURL('/logs');

	return username;
}

test('/me page shows account info', async ({ page, request }) => {
	const email = `${uniqueUsername()}@example.com`;
	const username = await registerAndLogin(page, request, { email });

	await page.goto('/me');

	await expect(page.getByRole('heading', { name: 'My Account' })).toBeVisible();
	await expect(page.locator('dd', { hasText: username })).toBeVisible();
	await expect(page.locator('dd', { hasText: email })).toBeVisible();
});

// --- Change Email ---

test('change email successfully', async ({ page, request }) => {
	const username = await registerAndLogin(page, request);
	const newEmail = `${uniqueUsername()}@example.com`;

	await page.goto('/me');
	await page.fill('input[name="new-email"]', newEmail);
	await page.click('button:has-text("Update Email")');

	await expect(page.getByText('Email updated successfully.')).toBeVisible();
	// The account info section should now show the new email
	await expect(page.getByText(newEmail)).toBeVisible();
});

test('clear email by submitting blank', async ({ page, request }) => {
	const email = `${uniqueUsername()}@example.com`;
	await registerAndLogin(page, request, { email });

	await page.goto('/me');

	// Verify email is shown in account info
	await expect(page.getByText(email)).toBeVisible();

	// Clear the email field and submit
	await page.fill('input[name="new-email"]', '');
	await page.click('button:has-text("Update Email")');

	await expect(page.getByText('Email updated successfully.')).toBeVisible();
	// The email should no longer appear in the account info section
	// (the dd element showing the email should be gone)
	await expect(page.locator('dt:has-text("Email")')).not.toBeVisible();
});

test('change email to duplicate shows error', async ({ page, request }) => {
	const takenEmail = `${uniqueUsername()}@example.com`;

	// Register another user with this email
	await request.post('/api/register', {
		data: { username: uniqueUsername(), email: takenEmail, password: testPassword },
	});

	await registerAndLogin(page, request);

	await page.goto('/me');
	await page.fill('input[name="new-email"]', takenEmail);
	await page.click('button:has-text("Update Email")');

	await expect(page.getByText('email already in use')).toBeVisible();
});

// --- Change Password ---

test('change password successfully', async ({ page, request }) => {
	const username = await registerAndLogin(page, request);
	const newPassword = 'newpassword456';

	await page.goto('/me');
	await page.fill('input[name="current-password"]', testPassword);
	await page.fill('input[name="new-password"]', newPassword);
	await page.click('button:has-text("Update Password")');

	await expect(page.getByText('Password updated successfully.')).toBeVisible();

	// Verify the new password works by logging out and back in
	await page.click('button:has-text("Logout")');
	await page.goto('/login');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', newPassword);
	await page.click('button[type="submit"]');
	await page.waitForURL('/logs');
});

test('change password with wrong current password shows error', async ({ page, request }) => {
	await registerAndLogin(page, request);

	await page.goto('/me');
	await page.fill('input[name="current-password"]', 'wrongpassword');
	await page.fill('input[name="new-password"]', 'newpassword456');
	await page.click('button:has-text("Update Password")');

	await expect(page.getByText('current password is incorrect')).toBeVisible();
});

test('change password clears form fields on success', async ({ page, request }) => {
	await registerAndLogin(page, request);

	await page.goto('/me');
	await page.fill('input[name="current-password"]', testPassword);
	await page.fill('input[name="new-password"]', 'newpassword456');
	await page.click('button:has-text("Update Password")');

	await expect(page.getByText('Password updated successfully.')).toBeVisible();

	// Form fields should be cleared after success
	await expect(page.locator('input[name="current-password"]')).toHaveValue('');
	await expect(page.locator('input[name="new-password"]')).toHaveValue('');
});
