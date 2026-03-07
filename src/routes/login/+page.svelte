<script>
	import { login, passkeyLogin, getAuth } from '$lib/auth.svelte.js';
	import { isWebAuthnSupported } from '$lib/passkeys.js';
	import { getSettings } from '$lib/settings.svelte.js';
	import { goto } from '$app/navigation';

	const auth = getAuth();
	const settings = getSettings();

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let submitting = $state(false);
	let passkeySubmitting = $state(false);
	let webauthnSupported = $state(false);

	$effect(() => {
		webauthnSupported = isWebAuthnSupported();
	});

	async function handleSubmit(e) {
		e.preventDefault();
		error = '';
		submitting = true;
		try {
			await login(username, password);
			goto('/logs');
		} catch (err) {
			error = err.message;
		} finally {
			submitting = false;
		}
	}

	async function handlePasskeyLogin() {
		error = '';
		passkeySubmitting = true;
		try {
			await passkeyLogin();
			goto('/logs');
		} catch (err) {
			error = err.message;
		} finally {
			passkeySubmitting = false;
		}
	}

	$effect(() => {
		if (!auth.loading && auth.isLoggedIn) {
			goto('/logs');
		}
	});
</script>

<div class="min-h-screen bg-gray-100 flex items-center justify-center">
	<div class="bg-white rounded-lg shadow-lg p-8 w-full max-w-sm">
		<h1 class="text-2xl font-bold text-gray-800 mb-6 text-center">Login</h1>

		{#if error}
			<p class="text-red-600 text-sm mb-4 text-center">{error}</p>
		{/if}

		<form onsubmit={handleSubmit} class="space-y-4">
			<div>
				<label for="username" class="block text-sm font-medium text-gray-700">Username</label>
				<input
					type="text"
					id="username"
					name="username"
					bind:value={username}
					required
					class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
				/>
			</div>

			<div>
				<label for="password" class="block text-sm font-medium text-gray-700">Password</label>
				<input
					type="password"
					id="password"
					name="password"
					bind:value={password}
					required
					class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
				/>
			</div>

			<button
				type="submit"
				disabled={submitting}
				class="w-full bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 disabled:opacity-50"
			>
				{submitting ? 'Logging in...' : 'Login'}
			</button>
		</form>

		<p class="mt-4 text-sm text-center text-gray-600">
			Don't have an account? <a href="/register" class="text-blue-600 hover:underline">Register</a>
		</p>

		{#if webauthnSupported && settings.passkeysEnabled}
			<div class="flex items-center mt-5 mb-1">
				<hr class="flex-1 border-gray-300" />
				<span class="px-3 text-gray-400 text-sm">or</span>
				<hr class="flex-1 border-gray-300" />
			</div>

			<button
				type="button"
				onclick={handlePasskeyLogin}
				disabled={passkeySubmitting}
				class="w-full border border-gray-300 text-gray-700 py-2 px-4 rounded hover:bg-gray-50 disabled:opacity-50"
			>
				{passkeySubmitting ? 'Verifying...' : 'Sign in with passkey'}
			</button>
		{/if}
	</div>
</div>
