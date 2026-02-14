<script>
	import { getAuth, changeEmail, changePassword } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';

	const auth = getAuth();

	// Change email state
	let newEmail = $state('');
	let emailError = $state('');
	let emailSuccess = $state('');
	let emailSubmitting = $state(false);

	// Change password state
	let currentPassword = $state('');
	let newPassword = $state('');
	let passwordError = $state('');
	let passwordSuccess = $state('');
	let passwordSubmitting = $state(false);

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) {
			goto('/login');
		}
	});

	$effect(() => {
		if (auth.user?.email) {
			newEmail = auth.user.email;
		}
	});

	async function handleChangeEmail(e) {
		e.preventDefault();
		emailError = '';
		emailSuccess = '';
		emailSubmitting = true;
		try {
			await changeEmail(newEmail);
			emailSuccess = 'Email updated successfully.';
		} catch (err) {
			emailError = err.message;
		} finally {
			emailSubmitting = false;
		}
	}

	async function handleChangePassword(e) {
		e.preventDefault();
		passwordError = '';
		passwordSuccess = '';
		passwordSubmitting = true;
		try {
			await changePassword(currentPassword, newPassword);
			passwordSuccess = 'Password updated successfully.';
			currentPassword = '';
			newPassword = '';
		} catch (err) {
			passwordError = err.message;
		} finally {
			passwordSubmitting = false;
		}
	}
</script>

{#if auth.loading}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Loading...</p>
	</div>
{:else if auth.isLoggedIn}
	<div class="min-h-screen bg-gray-100 py-12 flex flex-col items-center gap-6">
		<div class="bg-white rounded-lg shadow-lg p-8 w-full max-w-sm">
			<h1 class="text-2xl font-bold text-gray-800 mb-6 text-center">My Account</h1>
			<dl class="space-y-3">
				<div>
					<dt class="text-sm font-medium text-gray-500">Username</dt>
					<dd class="text-gray-900">{auth.user.username}</dd>
				</div>
				{#if auth.user.email}
					<div>
						<dt class="text-sm font-medium text-gray-500">Email</dt>
						<dd class="text-gray-900">{auth.user.email}</dd>
					</div>
				{/if}
			</dl>
		</div>

		<div class="bg-white rounded-lg shadow-lg p-8 w-full max-w-sm">
			<h2 class="text-lg font-bold text-gray-800 mb-4">Change Email</h2>

			{#if emailError}
				<p class="text-red-600 text-sm mb-4 text-center">{emailError}</p>
			{/if}
			{#if emailSuccess}
				<p class="text-green-600 text-sm mb-4 text-center">{emailSuccess}</p>
			{/if}

			<form onsubmit={handleChangeEmail} class="space-y-4">
				<div>
					<label for="new-email" class="block text-sm font-medium text-gray-700">
						New Email <span class="text-gray-400">(leave blank to remove)</span>
					</label>
					<input
						type="email"
						id="new-email"
						name="new-email"
						bind:value={newEmail}
						class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
					/>
				</div>

				<button
					type="submit"
					disabled={emailSubmitting}
					class="w-full bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 disabled:opacity-50"
				>
					{emailSubmitting ? 'Updating...' : 'Update Email'}
				</button>
			</form>
		</div>

		<div class="bg-white rounded-lg shadow-lg p-8 w-full max-w-sm">
			<h2 class="text-lg font-bold text-gray-800 mb-4">Change Password</h2>

			{#if passwordError}
				<p class="text-red-600 text-sm mb-4 text-center">{passwordError}</p>
			{/if}
			{#if passwordSuccess}
				<p class="text-green-600 text-sm mb-4 text-center">{passwordSuccess}</p>
			{/if}

			<form onsubmit={handleChangePassword} class="space-y-4">
				<div>
					<label for="current-password" class="block text-sm font-medium text-gray-700">Current Password</label>
					<input
						type="password"
						id="current-password"
						name="current-password"
						bind:value={currentPassword}
						required
						class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
					/>
				</div>

				<div>
					<label for="new-password" class="block text-sm font-medium text-gray-700">New Password</label>
					<input
						type="password"
						id="new-password"
						name="new-password"
						bind:value={newPassword}
						required
						minlength="8"
						class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
					/>
				</div>

				<button
					type="submit"
					disabled={passwordSubmitting}
					class="w-full bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 disabled:opacity-50"
				>
					{passwordSubmitting ? 'Updating...' : 'Update Password'}
				</button>
			</form>
		</div>
	</div>
{/if}
