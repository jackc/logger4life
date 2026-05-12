<script>
	import { getAuth } from '$lib/auth.svelte.js';
	import { apiGet, apiPost, apiPut } from '$lib/api.js';

	const auth = getAuth();

	let logs = $state([]);
	let loading = $state(true);
	let cardState = $state({});

	const pinnedLogs = $derived(
		logs
			.filter((l) => l.pinned_to_home)
			.sort((a, b) => a.home_position - b.home_position)
	);

	function buildInitialFieldValues(log) {
		const values = {};
		if (log.fields?.length > 0) {
			for (const f of log.fields) {
				values[f.name] = f.type === 'boolean' ? false : '';
			}
		}
		return values;
	}

	function initCardState(logsList) {
		const state = {};
		for (const log of logsList) {
			state[log.id] = {
				fieldValues: buildInitialFieldValues(log),
				logging: false,
				error: '',
				success: false,
			};
		}
		cardState = state;
	}

	async function fetchLogs() {
		loading = true;
		try {
			const data = await apiGet('/api/logs');
			logs = data || [];
			initCardState(logs);
		} catch {
			logs = [];
		} finally {
			loading = false;
		}
	}

	async function moveLog(log, direction) {
		const idx = pinnedLogs.findIndex((l) => l.id === log.id);
		const target = idx + direction;
		if (target < 0 || target >= pinnedLogs.length) return;
		try {
			await apiPut(`/api/logs/${log.id}/home-position`, { home_position: target });
			await fetchLogs();
		} catch {
			// ignore — next fetch will re-sync
		}
	}

	async function logEntry(log) {
		const state = cardState[log.id];
		state.logging = true;
		state.error = '';
		state.success = false;

		try {
			const payload = {};
			if (log.fields?.length > 0) {
				for (const f of log.fields) {
					const val = state.fieldValues[f.name];
					if (f.type === 'boolean') {
						payload[f.name] = !!val;
					} else if (val !== '' && val !== undefined && val !== null) {
						payload[f.name] = String(val);
					}
				}
			}
			await apiPost(`/api/logs/${log.id}/entries`, { fields: payload });
			state.fieldValues = buildInitialFieldValues(log);
			state.success = true;

			setTimeout(() => {
				if (cardState[log.id]) {
					cardState[log.id].success = false;
				}
			}, 1500);
		} catch (err) {
			state.error = err.message;
		} finally {
			state.logging = false;
		}
	}

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
{:else if !auth.isLoggedIn}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<div class="bg-white rounded-lg shadow-lg p-8 text-center max-w-sm w-full">
			<h1 class="text-3xl font-bold text-gray-800 mb-2">Logger4Life</h1>
			<p class="text-gray-600 mb-6">Quick event logging for your daily life. Track vitamins, pushups, diapers, and anything else.</p>
			<div class="space-y-3">
				<a href="/login" class="block w-full bg-blue-600 text-white py-2 px-4 rounded hover:bg-blue-700 text-center">
					Login
				</a>
				<a href="/register" class="block w-full border border-blue-600 text-blue-600 py-2 px-4 rounded hover:bg-blue-50 text-center">
					Register
				</a>
			</div>
		</div>
	</div>
{:else}
	<div class="min-h-screen bg-gray-100 p-6">
		<div class="max-w-lg mx-auto">
			<h1 class="text-2xl font-bold text-gray-800 mb-6">Quick Log</h1>

			{#if loading}
				<p class="text-gray-500">Loading logs...</p>
			{:else if logs.length === 0}
				<div class="bg-white rounded-lg shadow p-6 text-center">
					<p class="text-gray-600 mb-4">You don't have any logs yet.</p>
					<a href="/logs" class="text-blue-600 hover:underline font-medium">Create your first log</a>
				</div>
			{:else if pinnedLogs.length === 0}
				<div class="bg-white rounded-lg shadow p-6 text-center">
					<p class="text-gray-600 mb-4">No logs pinned to your home page.</p>
					<a href="/logs" class="text-blue-600 hover:underline font-medium">Pin logs on the logs page</a>
				</div>
			{:else}
				<div class="space-y-4">
					{#each pinnedLogs as log, idx (log.id)}
						{@const state = cardState[log.id]}
						{#if state}
							<div class="bg-white rounded-lg shadow p-4" data-testid="log-card">
								<div class="flex items-center justify-between mb-3 gap-2">
									<div class="min-w-0 flex-1">
										<h2 class="text-lg font-semibold text-gray-800 inline">{log.name}</h2>
										{#if !log.is_owner}
											<span class="text-xs text-gray-400 ml-1">(shared)</span>
										{/if}
									</div>
									<div class="flex items-center gap-1 text-sm">
										<button
											onclick={() => moveLog(log, -1)}
											disabled={idx === 0}
											class="text-gray-500 hover:text-blue-600 disabled:opacity-30 px-2"
											aria-label="Move up"
											data-testid="home-move-up"
										>
											↑
										</button>
										<button
											onclick={() => moveLog(log, 1)}
											disabled={idx === pinnedLogs.length - 1}
											class="text-gray-500 hover:text-blue-600 disabled:opacity-30 px-2"
											aria-label="Move down"
											data-testid="home-move-down"
										>
											↓
										</button>
										<a href="/logs/{log.id}" class="text-blue-600 hover:underline px-2">View entries</a>
									</div>
								</div>

								{#if log.fields?.length > 0}
									<form onsubmit={(e) => { e.preventDefault(); logEntry(log); }} class="space-y-3">
										{#each log.fields as field}
											<div>
												<label class="block text-sm font-medium text-gray-700 mb-1">
													{field.name}{#if field.required}<span class="text-red-500 ml-0.5">*</span>{/if}
												</label>
												{#if field.type === 'number'}
													<input
														type="number"
														step="any"
														name="field-{log.id}-{field.name}"
														bind:value={state.fieldValues[field.name]}
														placeholder={field.name}
														required={field.required}
														class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
													/>
												{:else if field.type === 'boolean'}
													<input
														type="checkbox"
														name="field-{log.id}-{field.name}"
														bind:checked={state.fieldValues[field.name]}
														class="rounded"
													/>
												{:else}
													<input
														type="text"
														name="field-{log.id}-{field.name}"
														bind:value={state.fieldValues[field.name]}
														placeholder={field.name}
														required={field.required}
														class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
													/>
												{/if}
											</div>
										{/each}
										<button
											type="submit"
											disabled={state.logging}
											class="w-full py-3 px-4 rounded-lg text-lg font-semibold disabled:opacity-50 {state.success ? 'bg-green-600 text-white' : 'bg-blue-600 text-white hover:bg-blue-700'}"
										>
											{#if state.logging}
												Logging...
											{:else if state.success}
												Logged!
											{:else}
												Log It!
											{/if}
										</button>
									</form>
								{:else}
									<button
										onclick={() => logEntry(log)}
										disabled={state.logging}
										class="w-full py-3 px-4 rounded-lg text-lg font-semibold disabled:opacity-50 {state.success ? 'bg-green-600 text-white' : 'bg-blue-600 text-white hover:bg-blue-700'}"
									>
										{#if state.logging}
											Logging...
										{:else if state.success}
											Logged!
										{:else}
											Log It!
										{/if}
									</button>
								{/if}

								{#if state.error}
									<p class="text-red-600 text-sm mt-2">{state.error}</p>
								{/if}
							</div>
						{/if}
					{/each}
				</div>
			{/if}
		</div>
	</div>
{/if}
