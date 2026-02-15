<script>
	import { page } from '$app/state';
	import { getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost, apiPut, apiDelete } from '$lib/api.js';

	const auth = getAuth();

	let log = $state(null);
	let entries = $state([]);
	let loading = $state(true);
	let logging = $state(false);
	let error = $state('');
	let fieldValues = $state({});

	let editingEntryId = $state(null);
	let editFields = $state({});
	let editOccurredAt = $state('');
	let editError = $state('');
	let saving = $state(false);

	let editing = $state(false);
	let editName = $state('');
	let editLogFields = $state([]);
	let editLogError = $state('');
	let editLogSaving = $state(false);

	let isOwner = $state(true);
	let shareToken = $state(null);
	let sharedUsers = $state([]);
	let showSharePanel = $state(false);
	let shareLoading = $state(false);
	let copied = $state(false);

	const logID = $derived(page.params.id);
	const hasFields = $derived(log?.fields?.length > 0);
	const isShared = $derived(!isOwner || sharedUsers.length > 0);

	function resetFieldValues() {
		if (log?.fields?.length > 0) {
			const initial = {};
			for (const f of log.fields) {
				initial[f.name] = f.type === 'boolean' ? false : '';
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
			isOwner = logData.is_owner;
			shareToken = logData.share_token || null;
			resetFieldValues();

			if (logData.is_owner) {
				fetchSharedUsers();
			}
		} catch {
			log = null;
			entries = [];
		} finally {
			loading = false;
		}
	}

	async function fetchSharedUsers() {
		try {
			sharedUsers = await apiGet(`/api/logs/${logID}/shares`);
		} catch {
			sharedUsers = [];
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
					if (f.type === 'boolean') {
						payload[f.name] = !!val;
					} else if (val !== '' && val !== undefined && val !== null) {
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

	function toLocalDatetimeString(date) {
		const pad = (n) => String(n).padStart(2, '0');
		return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
	}

	function startEditing(entry) {
		editingEntryId = entry.id;
		editError = '';
		if (log?.fields?.length > 0) {
			const initial = {};
			for (const f of log.fields) {
				const val = entry.fields?.[f.name];
				if (f.type === 'boolean') {
					initial[f.name] = val ?? false;
				} else {
					initial[f.name] = val != null ? String(val) : '';
				}
			}
			editFields = initial;
		} else {
			editFields = {};
		}
		editOccurredAt = toLocalDatetimeString(new Date(entry.occurred_at));
	}

	function cancelEditing() {
		editingEntryId = null;
		editError = '';
	}

	async function deleteLog() {
		if (!confirm('Delete this log and all its entries?')) return;
		try {
			await apiDelete(`/api/logs/${logID}`);
			goto('/logs');
		} catch (err) {
			error = err.message;
		}
	}

	async function deleteEntry(entry) {
		if (!confirm('Delete this entry?')) return;
		try {
			await apiDelete(`/api/logs/${logID}/entries/${entry.id}`);
			entries = entries.filter(e => e.id !== entry.id);
		} catch (err) {
			error = err.message;
		}
	}

	async function saveEntry(e) {
		if (e) e.preventDefault();
		saving = true;
		editError = '';
		try {
			const payload = {};
			if (hasFields) {
				for (const f of log.fields) {
					const val = editFields[f.name];
					if (f.type === 'boolean') {
						payload[f.name] = !!val;
					} else if (val !== '' && val !== undefined && val !== null) {
						payload[f.name] = String(val);
					}
				}
			}
			const occurredAt = new Date(editOccurredAt).toISOString();
			const updated = await apiPut(
				`/api/logs/${logID}/entries/${editingEntryId}`,
				{ fields: payload, occurred_at: occurredAt }
			);
			entries = entries.map(en => en.id === updated.id ? updated : en);
			editingEntryId = null;
		} catch (err) {
			editError = err.message;
		} finally {
			saving = false;
		}
	}

	async function generateShareToken() {
		shareLoading = true;
		try {
			const result = await apiPost(`/api/logs/${logID}/share-token`, {});
			shareToken = result.share_token;
		} catch (err) {
			error = err.message;
		} finally {
			shareLoading = false;
		}
	}

	async function revokeShareToken() {
		if (!confirm('Revoke the share link? New users will no longer be able to join.')) return;
		try {
			await apiDelete(`/api/logs/${logID}/share-token`);
			shareToken = null;
		} catch (err) {
			error = err.message;
		}
	}

	async function removeSharedUser(share) {
		if (!confirm(`Remove ${share.username}'s access?`)) return;
		try {
			await apiDelete(`/api/logs/${logID}/shares/${share.id}`);
			sharedUsers = sharedUsers.filter(s => s.id !== share.id);
		} catch (err) {
			error = err.message;
		}
	}

	function startEditingLog() {
		editName = log.name;
		editLogFields = log.fields.map(f => ({ name: f.name, type: f.type, required: f.required }));
		editLogError = '';
		editing = true;
	}

	function cancelEditingLog() {
		editing = false;
		editLogError = '';
	}

	function addEditField() {
		editLogFields = [...editLogFields, { name: '', type: 'number', required: false }];
	}

	function removeEditField(index) {
		editLogFields = editLogFields.filter((_, i) => i !== index);
	}

	async function saveLog(e) {
		if (e) e.preventDefault();
		editLogSaving = true;
		editLogError = '';
		try {
			const fields = editLogFields
				.filter((f) => f.name.trim() !== '')
				.map((f) => ({ name: f.name.trim(), type: f.type, required: f.required }));
			const updated = await apiPut(`/api/logs/${logID}`, { name: editName.trim(), fields });
			log = updated;
			isOwner = updated.is_owner;
			shareToken = updated.share_token || null;
			resetFieldValues();
			editing = false;
		} catch (err) {
			editLogError = err.message;
		} finally {
			editLogSaving = false;
		}
	}

	function copyShareLink() {
		const url = `${window.location.origin}/join/${shareToken}`;
		navigator.clipboard.writeText(url);
		copied = true;
		setTimeout(() => { copied = false; }, 1500);
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

			{#if editing}
				<form onsubmit={saveLog} class="bg-white rounded-lg shadow p-4 mt-2 mb-6 space-y-3">
					<div>
						<label class="block text-sm font-medium text-gray-700 mb-1">Log Name</label>
						<input
							type="text"
							bind:value={editName}
							required
							maxlength="100"
							class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
							data-testid="edit-log-name"
						/>
					</div>

					{#each editLogFields as field, i}
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
								onclick={() => removeEditField(i)}
								class="text-red-500 hover:text-red-700 px-2 py-2 text-lg leading-none"
							>
								&times;
							</button>
						</div>
					{/each}

					<button
						type="button"
						onclick={addEditField}
						class="text-blue-600 hover:text-blue-800 text-sm"
					>
						+ Add Field
					</button>

					{#if editLogError}
						<p class="text-red-600 text-sm">{editLogError}</p>
					{/if}

					<div class="flex gap-2">
						<button
							type="submit"
							disabled={editLogSaving}
							class="bg-blue-600 text-white py-2 px-4 rounded text-sm font-semibold hover:bg-blue-700 disabled:opacity-50"
							data-testid="save-log"
						>
							{editLogSaving ? 'Saving...' : 'Save'}
						</button>
						<button
							type="button"
							onclick={cancelEditingLog}
							disabled={editLogSaving}
							class="bg-gray-200 text-gray-700 py-2 px-4 rounded text-sm font-semibold hover:bg-gray-300 disabled:opacity-50"
						>
							Cancel
						</button>
					</div>
				</form>
			{:else}
				<div class="flex items-center justify-between mt-2 mb-6">
					<h1 class="text-2xl font-bold text-gray-800">{log.name}</h1>
					{#if isOwner}
						<div class="flex gap-3">
							<button
								onclick={startEditingLog}
								class="text-gray-400 hover:text-blue-600 text-sm"
								data-testid="edit-log"
							>
								Edit
							</button>
							<button
								onclick={() => showSharePanel = !showSharePanel}
								class="text-gray-400 hover:text-blue-600 text-sm"
							>
								Share
							</button>
							<button
								onclick={deleteLog}
								class="text-gray-400 hover:text-red-600 text-sm"
								data-testid="delete-log"
							>
								Delete Log
							</button>
						</div>
					{/if}
				</div>
			{/if}

			{#if showSharePanel && isOwner}
				<div class="bg-white rounded-lg shadow p-4 mb-6 space-y-4">
					<h2 class="text-sm font-semibold text-gray-700">Sharing</h2>

					{#if shareToken}
						<div class="space-y-2">
							<div class="flex gap-2">
								<input
									type="text"
									readonly
									value="{window.location.origin}/join/{shareToken}"
									class="flex-1 rounded border-gray-300 shadow-sm px-3 py-2 border text-sm bg-gray-50"
								/>
								<button
									onclick={copyShareLink}
									class="bg-blue-600 text-white py-2 px-3 rounded text-sm hover:bg-blue-700 whitespace-nowrap"
								>
									{copied ? 'Copied!' : 'Copy'}
								</button>
							</div>
							<button
								onclick={revokeShareToken}
								class="text-red-600 hover:text-red-800 text-sm"
							>
								Revoke link
							</button>
						</div>
					{:else}
						<button
							onclick={generateShareToken}
							disabled={shareLoading}
							class="bg-blue-600 text-white py-2 px-4 rounded text-sm hover:bg-blue-700 disabled:opacity-50"
						>
							{shareLoading ? 'Generating...' : 'Generate Share Link'}
						</button>
					{/if}

					{#if sharedUsers.length > 0}
						<div>
							<h3 class="text-xs font-medium text-gray-500 uppercase mb-2">Shared with</h3>
							<div class="space-y-2">
								{#each sharedUsers as share}
									<div class="flex items-center justify-between">
										<span class="text-sm text-gray-700">{share.username}</span>
										<button
											onclick={() => removeSharedUser(share)}
											class="text-gray-400 hover:text-red-600 text-sm"
										>
											Remove
										</button>
									</div>
								{/each}
							</div>
						</div>
					{:else}
						<p class="text-sm text-gray-500">No one has joined yet.</p>
					{/if}
				</div>
			{/if}

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
							{:else if field.type === 'boolean'}
								<input
									type="checkbox"
									name="field-{field.name}"
									bind:checked={fieldValues[field.name]}
									class="rounded"
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
							{#if editingEntryId === entry.id}
								<form onsubmit={saveEntry} class="space-y-3">
									<div>
										<label class="block text-sm font-medium text-gray-700 mb-1">Date & Time</label>
										<input
											type="datetime-local"
											bind:value={editOccurredAt}
											required
											class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
										/>
									</div>
									{#if hasFields}
										{#each log.fields as field}
											<div>
												<label class="block text-sm font-medium text-gray-700 mb-1">
													{field.name}{#if field.required}<span class="text-red-500 ml-0.5">*</span>{/if}
												</label>
												{#if field.type === 'number'}
													<input
														type="number"
														step="any"
														bind:value={editFields[field.name]}
														required={field.required}
														class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
													/>
												{:else if field.type === 'boolean'}
													<input
														type="checkbox"
														bind:checked={editFields[field.name]}
														class="rounded"
													/>
												{:else}
													<input
														type="text"
														bind:value={editFields[field.name]}
														required={field.required}
														class="w-full rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
													/>
												{/if}
											</div>
										{/each}
									{/if}
									{#if editError}
										<p class="text-red-600 text-sm">{editError}</p>
									{/if}
									<div class="flex gap-2">
										<button
											type="submit"
											disabled={saving}
											class="bg-blue-600 text-white py-2 px-4 rounded text-sm font-semibold hover:bg-blue-700 disabled:opacity-50"
										>
											{saving ? 'Saving...' : 'Save'}
										</button>
										<button
											type="button"
											onclick={cancelEditing}
											disabled={saving}
											class="bg-gray-200 text-gray-700 py-2 px-4 rounded text-sm font-semibold hover:bg-gray-300 disabled:opacity-50"
										>
											Cancel
										</button>
									</div>
								</form>
							{:else}
								<div class="flex items-start justify-between">
									<div>
										<div>
									{formatTimestamp(entry.occurred_at)}
									{#if isShared}
										<span class="text-xs text-gray-400 ml-1">by {entry.username}</span>
									{/if}
								</div>
										{#if entry.fields && Object.keys(entry.fields).length > 0}
											<div class="text-sm text-gray-500 mt-1">
												{#each Object.entries(entry.fields) as [name, value]}
													{@const def = log.fields.find(f => f.name === name)}
													<span class="mr-3">{name}: <span class="font-medium text-gray-700">{def?.type === 'boolean' ? (value ? 'Yes' : 'No') : value}</span></span>
												{/each}
											</div>
										{/if}
									</div>
									<div class="flex gap-2 ml-2 shrink-0">
										<button
											onclick={() => startEditing(entry)}
											class="text-gray-400 hover:text-blue-600 text-sm"
											data-testid="edit-entry"
										>
											Edit
										</button>
										<button
											onclick={() => deleteEntry(entry)}
											class="text-gray-400 hover:text-red-600 text-sm"
											data-testid="delete-entry"
										>
											Delete
										</button>
									</div>
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
