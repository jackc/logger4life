<script>
	import { getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost, apiDelete } from '$lib/api.js';

	const auth = getAuth();

	let logs = $state([]);
	let loading = $state(true);
	let newLogName = $state('');
	let newLogFields = $state([]);
	let error = $state('');
	let creating = $state(false);

	function addField() {
		newLogFields = [...newLogFields, { name: '', type: 'number', required: false }];
	}

	function removeField(index) {
		newLogFields = newLogFields.filter((_, i) => i !== index);
	}

	async function fetchLogs() {
		loading = true;
		try {
			logs = await apiGet('/api/logs');
		} catch {
			logs = [];
		} finally {
			loading = false;
		}
	}

	async function createLog(e) {
		e.preventDefault();
		error = '';
		creating = true;
		try {
			const fields = newLogFields
				.filter((f) => f.name.trim() !== '')
				.map((f) => ({ name: f.name.trim(), type: f.type, required: f.required }));
			const log = await apiPost('/api/logs', { name: newLogName.trim(), fields });
			newLogName = '';
			newLogFields = [];
			logs = [...logs, log].sort((a, b) =>
				a.name.toLowerCase().localeCompare(b.name.toLowerCase())
			);
		} catch (err) {
			error = err.message;
		} finally {
			creating = false;
		}
	}

	async function deleteLog(log) {
		if (!confirm('Delete this log and all its entries?')) return;
		try {
			await apiDelete(`/api/logs/${log.id}`);
			logs = logs.filter(l => l.id !== log.id);
		} catch (err) {
			error = err.message;
		}
	}

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) {
			goto('/login');
		}
	});

	$effect(() => {
		if (!auth.loading && auth.isLoggedIn) {
			fetchLogs();
		}
	});
</script>

{#if auth.loading}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Loading...</p>
	</div>
{:else if auth.isLoggedIn}
	<div class="min-h-screen bg-gray-100 p-6">
		<div class="max-w-lg mx-auto">
			<h1 class="text-2xl font-bold text-gray-800 mb-6">My Logs</h1>

			<form onsubmit={createLog} class="bg-white rounded-lg shadow p-4 mb-6 space-y-3">
				<div class="flex gap-3">
					<input
						type="text"
						name="log-name"
						bind:value={newLogName}
						placeholder="New log name..."
						required
						maxlength="100"
						class="flex-1 rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
					/>
					<button
						type="submit"
						disabled={creating}
						class="bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 disabled:opacity-50 whitespace-nowrap"
					>
						{creating ? 'Creating...' : 'Create Log'}
					</button>
				</div>

				{#each newLogFields as field, i}
					<div class="flex gap-2 items-center">
						<input
							type="text"
							bind:value={field.name}
							placeholder="Field name"
							maxlength="100"
							class="flex-1 rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border text-sm"
						/>
						<select
							bind:value={field.type}
							class="rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border text-sm"
						>
							<option value="number">Number</option>
							<option value="text">Text</option>
							<option value="boolean">Boolean</option>
						</select>
						<label class="flex items-center gap-1 text-sm text-gray-600 whitespace-nowrap">
							<input type="checkbox" bind:checked={field.required} class="rounded" />
							Required
						</label>
						<button
							type="button"
							onclick={() => removeField(i)}
							class="text-red-500 hover:text-red-700 px-2 py-2 text-lg leading-none"
						>
							&times;
						</button>
					</div>
				{/each}

				<button
					type="button"
					onclick={addField}
					class="text-blue-600 hover:text-blue-800 text-sm"
				>
					+ Add Field
				</button>
			</form>

			{#if error}
				<p class="text-red-600 text-sm mb-4">{error}</p>
			{/if}

			{#if loading}
				<p class="text-gray-500">Loading logs...</p>
			{:else if logs.length === 0}
				<p class="text-gray-500">No logs yet. Create one above to get started.</p>
			{:else}
				<div class="space-y-2">
					{#each logs as log}
						<div class="bg-white rounded-lg shadow p-4 flex items-center justify-between">
							<div>
								<a
									href="/logs/{log.id}"
									class="text-gray-800 font-medium hover:text-blue-600 transition-colors"
								>
									{log.name}
								</a>
								{#if !log.is_owner}
									<span class="text-xs text-gray-400 ml-2">(shared)</span>
								{/if}
							</div>
							{#if log.is_owner}
								<button
									onclick={() => deleteLog(log)}
									class="text-gray-400 hover:text-red-600 text-sm"
									data-testid="delete-log"
								>
									Delete
								</button>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</div>
	</div>
{/if}
