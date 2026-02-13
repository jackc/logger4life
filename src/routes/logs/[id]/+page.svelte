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
	let fieldValues = $state({});

	const logID = $derived(page.params.id);
	const hasFields = $derived(log?.fields?.length > 0);

	function resetFieldValues() {
		if (log?.fields?.length > 0) {
			const initial = {};
			for (const f of log.fields) {
				initial[f.name] = '';
			}
			fieldValues = initial;
		} else {
			fieldValues = {};
		}
	}

	async function fetchData() {
		loading = true;
		try {
			const [logData, entriesData] = await Promise.all([
				apiGet(`/api/logs/${logID}`),
				apiGet(`/api/logs/${logID}/entries`)
			]);
			log = logData;
			entries = entriesData;
			resetFieldValues();
		} catch {
			log = null;
			entries = [];
		} finally {
			loading = false;
		}
	}

	async function logEntry(e) {
		if (e) e.preventDefault();
		logging = true;
		error = '';
		try {
			const payload = {};
			if (hasFields) {
				for (const f of log.fields) {
					const val = fieldValues[f.name];
					if (val !== '' && val !== undefined && val !== null) {
						payload[f.name] = String(val);
					}
				}
			}
			const entry = await apiPost(`/api/logs/${logID}/entries`, { fields: payload });
			entries = [entry, ...entries];
			resetFieldValues();
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

			{#if hasFields}
				<form onsubmit={logEntry} class="bg-white rounded-lg shadow p-4 mb-6 space-y-3">
					{#each log.fields as field}
						<div>
							<label class="block text-sm font-medium text-gray-700 mb-1">
								{field.name}{#if field.required}<span class="text-red-500 ml-0.5">*</span>{/if}
							</label>
							{#if field.type === 'number'}
								<input
									type="number"
									step="any"
									name="field-{field.name}"
									bind:value={fieldValues[field.name]}
									placeholder={field.name}
									required={field.required}
									class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
								/>
							{:else}
								<input
									type="text"
									name="field-{field.name}"
									bind:value={fieldValues[field.name]}
									placeholder={field.name}
									required={field.required}
									class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
								/>
							{/if}
						</div>
					{/each}
					<button
						type="submit"
						disabled={logging}
						class="w-full bg-blue-600 text-white py-4 px-6 rounded-lg text-xl font-semibold hover:bg-blue-700 disabled:opacity-50"
					>
						{logging ? 'Logging...' : 'Log It!'}
					</button>
				</form>
			{:else}
				<button
					onclick={logEntry}
					disabled={logging}
					class="w-full bg-blue-600 text-white py-4 px-6 rounded-lg text-xl font-semibold hover:bg-blue-700 disabled:opacity-50 mb-6"
				>
					{logging ? 'Logging...' : 'Log It!'}
				</button>
			{/if}

			{#if error}
				<p class="text-red-600 text-sm mb-4">{error}</p>
			{/if}

			{#if entries.length === 0}
				<p class="text-gray-500">No entries yet. Tap the button above to log one.</p>
			{:else}
				<div class="bg-white rounded-lg shadow divide-y">
					{#each entries as entry}
						<div class="px-4 py-3 text-gray-700" data-testid="log-entry">
							<div>{formatTimestamp(entry.created_at)}</div>
							{#if entry.fields && Object.keys(entry.fields).length > 0}
								<div class="text-sm text-gray-500 mt-1">
									{#each Object.entries(entry.fields) as [name, value]}
										<span class="mr-3">{name}: <span class="font-medium text-gray-700">{value}</span></span>
									{/each}
								</div>
							{/if}
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
