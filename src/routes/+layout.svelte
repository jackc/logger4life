<script>
	let { children } = $props();
	import "../app.css";
	import { getAuth, checkAuth, logout } from '$lib/auth.svelte.js';
	import { loadSettings } from '$lib/settings.svelte.js';

	const auth = getAuth();

	$effect(() => {
		checkAuth();
		loadSettings();
	});
</script>

<nav class="bg-white shadow-sm px-4 py-2 flex items-center justify-between">
	<a href="/" class="font-bold text-lg text-blue-600">Logger4Life</a>
	<div class="flex gap-4 items-center">
		{#if auth.loading}
			<span class="text-gray-400">...</span>
		{:else if auth.isLoggedIn}
			<a href="/logs" class="text-gray-700 hover:text-blue-600">My Logs</a>
			<a href="/me" class="text-gray-700 hover:text-blue-600">{auth.user.username}</a>
			<button onclick={() => logout()} class="text-gray-500 hover:text-red-600">Logout</button>
		{:else}
			<a href="/login" class="text-gray-700 hover:text-blue-600">Login</a>
			<a href="/register" class="text-blue-600 hover:text-blue-800 font-medium">Register</a>
		{/if}
	</div>
</nav>

<main>
	{@render children()}
</main>
