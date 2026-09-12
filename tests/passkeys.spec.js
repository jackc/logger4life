// @ts-check
import { test, expect } from '@playwright/test';

function uniqueUsername() {
	return 'test_' + Date.now() + '_' + Math.random().toString(36).slice(2, 8);
}

const testPassword = 'password123';

async function addVirtualAuthenticator(page) {
	const cdp = await page.context().newCDPSession(page);
	await cdp.send('WebAuthn.enable');
	const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
		options: {
			protocol: 'ctap2',
			transport: 'internal',
			hasResidentKey: true,
			hasUserVerification: true,
			isUserVerified: true,
		},
	});
	return { cdp, authenticatorId };
}

async function registerUser(page, request, username) {
	await request.post('/api/register', {
		data: { username, password: testPassword },
	});
	await page.goto('/login');
	await page.fill('input[name="username"]', username);
	await page.fill('input[name="password"]', testPassword);
	await page.click('button[type="submit"]');
	await page.waitForURL('/logs');
}

test('login page shows passkey button', async ({ page }) => {
	await page.goto('/login');

	await expect(page.getByRole('button', { name: 'Sign in with passkey' })).toBeVisible();
});

test('register a passkey from account page', async ({ page, request }) => {
	const username = uniqueUsername();
	const { cdp } = await addVirtualAuthenticator(page);

	await registerUser(page, request, username);

	await page.goto('/me');
	await expect(page.getByRole('heading', { name: 'Passkeys' })).toBeVisible();
	await expect(page.getByText('No passkeys registered yet.')).toBeVisible();

	// Fill description and add passkey
	await page.fill('input[name="passkey-description"]', 'Test Key');
	await page.getByRole('button', { name: 'Add passkey' }).click();

	await expect(page.getByText('Passkey added.')).toBeVisible();
	await expect(page.getByText('Test Key')).toBeVisible();
	await expect(page.getByText('No passkeys registered yet.')).not.toBeVisible();
});

test('login with passkey after registering one', async ({ page, request }) => {
	const username = uniqueUsername();
	const { cdp } = await addVirtualAuthenticator(page);

	// Register user and add a passkey
	await registerUser(page, request, username);
	await page.goto('/me');
	await page.fill('input[name="passkey-description"]', 'Login Key');
	await page.getByRole('button', { name: 'Add passkey' }).click();
	await expect(page.getByText('Passkey added.')).toBeVisible();

	// Log out
	await page.getByRole('button', { name: 'Logout' }).click();

	// Log in with passkey
	await page.goto('/login');
	await page.getByRole('button', { name: 'Sign in with passkey' }).click();

	await page.waitForURL('/logs');
	await expect(page.getByRole('heading', { name: 'My Logs' })).toBeVisible();
});

test('delete a passkey', async ({ page, request }) => {
	const username = uniqueUsername();
	const { cdp } = await addVirtualAuthenticator(page);

	await registerUser(page, request, username);
	await page.goto('/me');
	await page.fill('input[name="passkey-description"]', 'Delete Me');
	await page.getByRole('button', { name: 'Add passkey' }).click();
	await expect(page.getByText('Passkey added.')).toBeVisible();

	// Delete the passkey
	await page.getByRole('button', { name: 'Remove' }).click();

	await expect(page.getByText('Passkey removed.')).toBeVisible();
	await expect(page.getByText('No passkeys registered yet.')).toBeVisible();
});

test('edit passkey description', async ({ page, request }) => {
	const username = uniqueUsername();
	const { cdp } = await addVirtualAuthenticator(page);

	await registerUser(page, request, username);
	await page.goto('/me');
	await page.fill('input[name="passkey-description"]', 'Old Name');
	await page.getByRole('button', { name: 'Add passkey' }).click();
	await expect(page.getByText('Passkey added.')).toBeVisible();

	// Click the passkey name to start editing
	await page.getByRole('button', { name: 'Old Name' }).click();

	// Clear and type new name
	const input = page.locator('li input[type="text"]');
	await input.fill('New Name');
	await page.getByRole('button', { name: 'Save' }).click();

	await expect(page.getByRole('button', { name: 'New Name' })).toBeVisible();
});

// Reject one browser prompt, then restore the real API so the retry exercises
// SimpleWebAuthn and the backend with the virtual authenticator.
async function rejectNextPrompt(page, method, name) {
	await page.evaluate(({ method, name }) => {
		const original = navigator.credentials[method].bind(navigator.credentials);
		navigator.credentials[method] = async (...args) => {
			navigator.credentials[method] = original;
			throw new DOMException('Raw browser error details', name);
		};
	}, { method, name });
}

for (const name of ['NotAllowedError', 'AbortError']) {
	const message = name === 'AbortError'
		? 'The passkey request was canceled. Please try again.'
		: 'The passkey request was canceled, timed out, or was not allowed. Please try again.';

	test(`passkey registration can retry after ${name}`, async ({ page, request }) => {
		await addVirtualAuthenticator(page);
		await registerUser(page, request, uniqueUsername());
		await page.goto('/me');
		await page.fill('input[name="passkey-description"]', 'Retry Key');
		await rejectNextPrompt(page, 'create', name);
		const add = page.getByRole('button', { name: 'Add passkey' });
		await add.click();
		await expect(page.getByText(message, { exact: true })).toBeVisible();
		await expect(add).toBeEnabled();
		await expect(page.locator('input[name="passkey-description"]')).toHaveValue('Retry Key');
		await expect(page.getByText('No passkeys registered yet.')).toBeVisible();
		await add.click();
		await expect(page.getByText('Passkey added.')).toBeVisible();
		await expect(page.getByText(message, { exact: true })).toHaveCount(0);
		await expect(page.getByRole('button', { name: 'Retry Key', exact: true })).toBeVisible();
	});

	test(`passkey login can retry after ${name}`, async ({ page, request }) => {
		await addVirtualAuthenticator(page);
		await registerUser(page, request, uniqueUsername());
		await page.goto('/me');
		await page.getByRole('button', { name: 'Add passkey' }).click();
		await expect(page.getByText('Passkey added.')).toBeVisible();
		await page.getByRole('button', { name: 'Logout' }).click();
		await page.goto('/login');
		await rejectNextPrompt(page, 'get', name);
		const login = page.getByRole('button', { name: 'Sign in with passkey' });
		await login.click();
		await expect(page.getByText(message, { exact: true })).toBeVisible();
		await expect(login).toBeEnabled();
		await expect(page).toHaveURL(/\/login$/);
		await login.click();
		await page.waitForURL('/logs');
		await expect(page.getByRole('heading', { name: 'My Logs' })).toBeVisible();
	});
}

test('duplicate passkey registration explains how to recover', async ({ page, request }) => {
	await addVirtualAuthenticator(page);
	await registerUser(page, request, uniqueUsername());
	await page.goto('/me');
	await page.fill('input[name="passkey-description"]', 'Original Key');
	const add = page.getByRole('button', { name: 'Add passkey' });
	await add.click();
	await expect(page.getByText('Passkey added.')).toBeVisible();
	await page.fill('input[name="passkey-description"]', 'Duplicate Key');
	await add.click();
	await expect(page.getByText('This passkey is already registered to your account. Try a different passkey.')).toBeVisible();
	await expect(add).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Original Key', exact: true })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Duplicate Key', exact: true })).toHaveCount(0);
});
