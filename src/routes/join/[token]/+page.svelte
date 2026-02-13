<script>
	import { page } from '$app/state';
	import { getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost } from '$lib/api.js';

	const auth = getAuth();

	let info = $state(null);
	let loading = $state(true);
	let joining = $state(false);
	let error = $state('');

	const token = $derived(page.params.token);

	async function fetchInfo() {
		loading = true;
		error = '';
		try {
			info = await apiGet(`/api/join/${token}`);
		} catch (err) {
			error = err.message;
			info = null;
		} finally {
			loading = false;
		}
	}

	async function joinLog() {
		joining = true;
		error = '';
		try {
			const result = await apiPost(`/api/join/${token}`, {});
			goto(`/logs/${result.log_id}`);
		} catch (err) {
			error = err.message;
		} finally {
			joining = false;
		}
	}

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) {
			goto('/login');
		}
	});

	$effect(() => {
		if (!auth.loading && auth.isLoggedIn) {
			fetchInfo();
		}
	});
</script>

{#if auth.loading || loading}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Loading...</p>
	</div>
{:else if error && !info}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<div class="bg-white rounded-lg shadow-lg p-8 text-center max-w-sm w-full">
			<h1 class="text-xl font-bold text-gray-800 mb-4">Invalid Share Link</h1>
			<p class="text-gray-600 mb-6">This share link is not valid or has been revoked.</p>
			<a href="/" class="text-blue-600 hover:underline">Go home</a>
		</div>
	</div>
{:else if info}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<div class="bg-white rounded-lg shadow-lg p-8 text-center max-w-sm w-full">
			{#if info.is_owner}
				<h1 class="text-xl font-bold text-gray-800 mb-4">You own this log</h1>
				<p class="text-gray-600 mb-6">You are the owner of <span class="font-semibold">{info.log_name}</span>.</p>
				<a href="/logs/{info.log_id}" class="text-blue-600 hover:underline font-medium">Go to log</a>
			{:else if info.already_member}
				<h1 class="text-xl font-bold text-gray-800 mb-4">Already joined</h1>
				<p class="text-gray-600 mb-6">You already have access to <span class="font-semibold">{info.log_name}</span>.</p>
				<a href="/logs/{info.log_id}" class="text-blue-600 hover:underline font-medium">Go to log</a>
			{:else}
				<h1 class="text-xl font-bold text-gray-800 mb-2">Join Log</h1>
				<p class="text-gray-600 mb-6">
					<span class="font-semibold">{info.owner_username}</span> has invited you to join
					<span class="font-semibold">{info.log_name}</span>.
				</p>
				{#if error}
					<p class="text-red-600 text-sm mb-4">{error}</p>
				{/if}
				<button
					onclick={joinLog}
					disabled={joining}
					class="w-full bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 disabled:opacity-50"
				>
					{joining ? 'Joining...' : 'Join Log'}
				</button>
			{/if}
		</div>
	</div>
{/if}
