<script>
	import { page } from '$app/state';
	import { getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost } from '$lib/api.js';

	const auth = getAuth();

	let log = $state(null);
	let entries = $state([]);
	let loading = $state(true);
	let logging = $state(false);
	let error = $state('');

	const logID = $derived(page.params.id);

	async function fetchData() {
		loading = true;
		try {
			const [logData, entriesData] = await Promise.all([
				apiGet(`/api/logs/${logID}`),
				apiGet(`/api/logs/${logID}/entries`)
			]);
			log = logData;
			entries = entriesData;
		} catch {
			log = null;
			entries = [];
		} finally {
			loading = false;
		}
	}

	async function logEntry() {
		logging = true;
		error = '';
		try {
			const entry = await apiPost(`/api/logs/${logID}/entries`, {});
			entries = [entry, ...entries];
		} catch (err) {
			error = err.message;
		} finally {
			logging = false;
		}
	}

	function formatTimestamp(iso) {
		return new Date(iso).toLocaleString();
	}

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) {
			goto('/login');
		}
	});

	$effect(() => {
		if (!auth.loading && auth.isLoggedIn) {
			fetchData();
		}
	});
</script>

{#if auth.loading || loading}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Loading...</p>
	</div>
{:else if log}
	<div class="min-h-screen bg-gray-100 p-6">
		<div class="max-w-lg mx-auto">
			<a href="/logs" class="text-blue-600 hover:underline text-sm">&larr; Back to logs</a>

			<h1 class="text-2xl font-bold text-gray-800 mt-2 mb-6">{log.name}</h1>

			<button
				onclick={logEntry}
				disabled={logging}
				class="w-full bg-blue-600 text-white py-4 px-6 rounded-lg text-xl font-semibold hover:bg-blue-700 disabled:opacity-50 mb-6"
			>
				{logging ? 'Logging...' : 'Log It!'}
			</button>

			{#if error}
				<p class="text-red-600 text-sm mb-4">{error}</p>
			{/if}

			{#if entries.length === 0}
				<p class="text-gray-500">No entries yet. Tap the button above to log one.</p>
			{:else}
				<div class="bg-white rounded-lg shadow divide-y">
					{#each entries as entry}
						<div class="px-4 py-3 text-gray-700" data-testid="log-entry">
							{formatTimestamp(entry.created_at)}
						</div>
					{/each}
				</div>
			{/if}
		</div>
	</div>
{:else}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Log not found.</p>
	</div>
{/if}
