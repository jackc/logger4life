<script>
	import { register, getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';

	const auth = getAuth();

	let username = $state('');
	let email = $state('');
	let password = $state('');
	let error = $state('');
	let submitting = $state(false);

	async function handleSubmit(e) {
		e.preventDefault();
		error = '';
		submitting = true;
		try {
			await register(username, email, password);
			goto('/logs');
		} catch (err) {
			error = err.message;
		} finally {
			submitting = false;
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
		<h1 class="text-2xl font-bold text-gray-800 mb-6 text-center">Register</h1>

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
					maxlength="30"
					class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
				/>
			</div>

			<div>
				<label for="email" class="block text-sm font-medium text-gray-700">Email <span class="text-gray-400">(optional)</span></label>
				<input
					type="email"
					id="email"
					name="email"
					bind:value={email}
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
					minlength="8"
					class="mt-1 block w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
				/>
			</div>

			<button
				type="submit"
				disabled={submitting}
				class="w-full bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 disabled:opacity-50"
			>
				{submitting ? 'Registering...' : 'Register'}
			</button>
		</form>

		<p class="mt-4 text-sm text-center text-gray-600">
			Already have an account? <a href="/login" class="text-blue-600 hover:underline">Login</a>
		</p>
	</div>
</div>
