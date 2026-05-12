<script>
	import { getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost, apiPut, apiDelete } from '$lib/api.js';

	const auth = getAuth();

	let logs = $state([]);
	let folders = $state([]);
	let loading = $state(true);
	let newLogName = $state('');
	let newLogFields = $state([]);
	let error = $state('');
	let creating = $state(false);

	let newFolderName = $state('');
	let newFolderParent = $state('');
	let creatingFolder = $state(false);

	let movePicker = $state(null); // { kind: 'log'|'folder', id, currentParent }

	let renamingFolderId = $state('');
	let renameValue = $state('');

	let expanded = $state({});

	const EXPAND_KEY = 'logger4life:expandedFolders';

	function loadExpanded() {
		if (typeof localStorage === 'undefined') return;
		try {
			const raw = localStorage.getItem(EXPAND_KEY);
			if (raw) expanded = JSON.parse(raw);
		} catch {
			expanded = {};
		}
	}

	function saveExpanded() {
		if (typeof localStorage === 'undefined') return;
		localStorage.setItem(EXPAND_KEY, JSON.stringify(expanded));
	}

	function toggleFolder(id) {
		expanded = { ...expanded, [id]: !expanded[id] };
		saveExpanded();
	}

	function addField() {
		newLogFields = [...newLogFields, { name: '', type: 'number', required: false }];
	}

	function removeField(index) {
		newLogFields = newLogFields.filter((_, i) => i !== index);
	}

	async function fetchAll() {
		loading = true;
		try {
			const [logsData, foldersData] = await Promise.all([
				apiGet('/api/logs'),
				apiGet('/api/folders'),
			]);
			logs = logsData || [];
			folders = foldersData || [];
		} catch {
			logs = [];
			folders = [];
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
			await apiPost('/api/logs', { name: newLogName.trim(), fields });
			newLogName = '';
			newLogFields = [];
			await fetchAll();
		} catch (err) {
			error = err.message;
		} finally {
			creating = false;
		}
	}

	async function createFolder(e) {
		e.preventDefault();
		error = '';
		creatingFolder = true;
		try {
			await apiPost('/api/folders', {
				name: newFolderName.trim(),
				parent_folder_id: newFolderParent || null,
			});
			newFolderName = '';
			newFolderParent = '';
			await fetchAll();
		} catch (err) {
			error = err.message;
		} finally {
			creatingFolder = false;
		}
	}

	async function deleteLog(log) {
		if (!confirm('Delete this log and all its entries?')) return;
		try {
			await apiDelete(`/api/logs/${log.id}`);
			await fetchAll();
		} catch (err) {
			error = err.message;
		}
	}

	async function deleteFolder(folder) {
		if (!confirm(`Delete folder "${folder.name}"? It must be empty.`)) return;
		try {
			await apiDelete(`/api/folders/${folder.id}`);
			await fetchAll();
		} catch (err) {
			error = err.message;
		}
	}

	function startRename(folder) {
		renamingFolderId = folder.id;
		renameValue = folder.name;
	}

	async function commitRename() {
		const name = renameValue.trim();
		if (!name) {
			renamingFolderId = '';
			return;
		}
		try {
			await apiPut(`/api/folders/${renamingFolderId}`, { name });
			renamingFolderId = '';
			renameValue = '';
			await fetchAll();
		} catch (err) {
			error = err.message;
		}
	}

	async function moveLog(log, direction) {
		const siblings = logs
			.filter((l) => (l.folder_id || null) === (log.folder_id || null))
			.sort((a, b) => a.position - b.position);
		const idx = siblings.findIndex((l) => l.id === log.id);
		const target = idx + direction;
		if (target < 0 || target >= siblings.length) return;
		try {
			await apiPut(`/api/logs/${log.id}/placement`, {
				folder_id: log.folder_id,
				position: target,
			});
			await fetchAll();
		} catch (err) {
			error = err.message;
		}
	}

	async function moveFolder(folder, direction) {
		const siblings = folders
			.filter((f) => (f.parent_folder_id || null) === (folder.parent_folder_id || null))
			.sort((a, b) => a.position - b.position);
		const idx = siblings.findIndex((f) => f.id === folder.id);
		const target = idx + direction;
		if (target < 0 || target >= siblings.length) return;
		try {
			await apiPut(`/api/folders/${folder.id}/move`, {
				parent_folder_id: folder.parent_folder_id,
				position: target,
			});
			await fetchAll();
		} catch (err) {
			error = err.message;
		}
	}

	function openMovePicker(kind, item) {
		movePicker = {
			kind,
			id: item.id,
			currentParent: kind === 'log' ? item.folder_id : item.parent_folder_id,
		};
	}

	async function pickDestination(destFolderId) {
		const { kind, id } = movePicker;
		const url = kind === 'log' ? `/api/logs/${id}/placement` : `/api/folders/${id}/move`;
		const body =
			kind === 'log'
				? { folder_id: destFolderId, position: 99999 }
				: { parent_folder_id: destFolderId, position: 99999 };
		try {
			await apiPut(url, body);
			movePicker = null;
			await fetchAll();
		} catch (err) {
			error = err.message;
		}
	}

	// Folders that are not descendants of the folder being moved (for the picker).
	function eligibleFolders() {
		if (!movePicker) return [];
		if (movePicker.kind === 'log') return folders;
		// Exclude self and descendants when moving a folder.
		const excluded = new Set([movePicker.id]);
		let changed = true;
		while (changed) {
			changed = false;
			for (const f of folders) {
				if (!excluded.has(f.id) && f.parent_folder_id && excluded.has(f.parent_folder_id)) {
					excluded.add(f.id);
					changed = true;
				}
			}
		}
		return folders.filter((f) => !excluded.has(f.id));
	}

	function childFolders(parentID) {
		return folders
			.filter((f) => (f.parent_folder_id || null) === parentID)
			.sort((a, b) => a.position - b.position);
	}

	function childLogs(folderID) {
		return logs
			.filter((l) => (l.folder_id || null) === folderID)
			.sort((a, b) => a.position - b.position);
	}

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) {
			goto('/login');
		}
	});

	$effect(() => {
		if (!auth.loading && auth.isLoggedIn) {
			loadExpanded();
			fetchAll();
		}
	});
</script>

{#snippet logRow(log, depth)}
	{@const siblings = childLogs(log.folder_id || null)}
	{@const idx = siblings.findIndex((l) => l.id === log.id)}
	<div
		class="bg-white rounded-lg shadow p-3 flex items-center justify-between gap-2"
		style="margin-left: {depth * 1.5}rem"
		data-testid="log-row"
	>
		<a
			href="/logs/{log.id}"
			class="flex-1 min-w-0 text-gray-800 font-medium hover:text-blue-600 transition-colors truncate"
		>
			{log.name}
			{#if !log.is_owner}
				<span class="text-xs text-gray-400 ml-1">(shared)</span>
			{/if}
		</a>
		<div class="flex items-center gap-1 text-sm">
			<button
				onclick={() => moveLog(log, -1)}
				disabled={idx === 0}
				class="text-gray-500 hover:text-blue-600 disabled:opacity-30 px-2"
				aria-label="Move up"
				data-testid="move-up"
			>
				↑
			</button>
			<button
				onclick={() => moveLog(log, 1)}
				disabled={idx === siblings.length - 1}
				class="text-gray-500 hover:text-blue-600 disabled:opacity-30 px-2"
				aria-label="Move down"
				data-testid="move-down"
			>
				↓
			</button>
			<button
				onclick={() => openMovePicker('log', log)}
				class="text-gray-500 hover:text-blue-600 px-2"
				data-testid="move-to"
			>
				Move to…
			</button>
			{#if log.is_owner}
				<button
					onclick={() => deleteLog(log)}
					class="text-gray-400 hover:text-red-600 px-2"
					data-testid="delete-log"
				>
					Delete
				</button>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet folderRow(folder, depth)}
	{@const siblings = childFolders(folder.parent_folder_id || null)}
	{@const idx = siblings.findIndex((f) => f.id === folder.id)}
	{@const isOpen = expanded[folder.id] !== false}
	<div
		class="bg-gray-50 rounded-lg p-3 flex items-center justify-between gap-2 border border-gray-200"
		style="margin-left: {depth * 1.5}rem"
		data-testid="folder-row"
	>
		<div class="flex items-center gap-2 flex-1 min-w-0">
			<button
				onclick={() => toggleFolder(folder.id)}
				class="text-gray-500 hover:text-blue-600 w-5 text-center"
				aria-label={isOpen ? 'Collapse' : 'Expand'}
			>
				{isOpen ? '▾' : '▸'}
			</button>
			{#if renamingFolderId === folder.id}
				<input
					type="text"
					bind:value={renameValue}
					onblur={commitRename}
					onkeydown={(e) => e.key === 'Enter' && commitRename()}
					class="flex-1 rounded border-gray-300 px-2 py-1 border text-sm"
				/>
			{:else}
				<button
					onclick={() => startRename(folder)}
					class="text-gray-800 font-medium flex-1 min-w-0 truncate text-left"
					data-testid="folder-name"
				>
					{folder.name}
				</button>
			{/if}
		</div>
		<div class="flex items-center gap-1 text-sm">
			<button
				onclick={() => moveFolder(folder, -1)}
				disabled={idx === 0}
				class="text-gray-500 hover:text-blue-600 disabled:opacity-30 px-2"
				aria-label="Move up"
			>
				↑
			</button>
			<button
				onclick={() => moveFolder(folder, 1)}
				disabled={idx === siblings.length - 1}
				class="text-gray-500 hover:text-blue-600 disabled:opacity-30 px-2"
				aria-label="Move down"
			>
				↓
			</button>
			<button
				onclick={() => openMovePicker('folder', folder)}
				class="text-gray-500 hover:text-blue-600 px-2"
			>
				Move to…
			</button>
			<button
				onclick={() => deleteFolder(folder)}
				class="text-gray-400 hover:text-red-600 px-2"
				data-testid="delete-folder"
			>
				Delete
			</button>
		</div>
	</div>
{/snippet}

{#snippet tree(parentID, depth)}
	{#each childFolders(parentID) as folder (folder.id)}
		{@render folderRow(folder, depth)}
		{#if expanded[folder.id] !== false}
			{@render tree(folder.id, depth + 1)}
		{/if}
	{/each}
	{#each childLogs(parentID) as log (log.id)}
		{@render logRow(log, depth)}
	{/each}
{/snippet}

{#if auth.loading}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Loading...</p>
	</div>
{:else if auth.isLoggedIn}
	<div class="min-h-screen bg-gray-100 p-6">
		<div class="max-w-2xl mx-auto">
			<h1 class="text-2xl font-bold text-gray-800 mb-6">My Logs</h1>

			<form onsubmit={createLog} class="bg-white rounded-lg shadow p-4 mb-4 space-y-3">
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

			<form onsubmit={createFolder} class="bg-white rounded-lg shadow p-4 mb-6">
				<div class="flex gap-3 flex-wrap">
					<input
						type="text"
						bind:value={newFolderName}
						placeholder="New folder name..."
						required
						maxlength="100"
						class="flex-1 min-w-0 rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
					/>
					<select
						bind:value={newFolderParent}
						class="rounded border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 px-3 py-2 border"
					>
						<option value="">(root)</option>
						{#each folders as f}
							<option value={f.id}>{f.name}</option>
						{/each}
					</select>
					<button
						type="submit"
						disabled={creatingFolder}
						class="border border-blue-600 text-blue-600 py-2 px-4 rounded hover:bg-blue-50 disabled:opacity-50 whitespace-nowrap"
						data-testid="create-folder"
					>
						{creatingFolder ? 'Creating...' : 'New Folder'}
					</button>
				</div>
			</form>

			{#if error}
				<p class="text-red-600 text-sm mb-4">{error}</p>
			{/if}

			{#if loading}
				<p class="text-gray-500">Loading...</p>
			{:else if logs.length === 0 && folders.length === 0}
				<p class="text-gray-500">No logs yet. Create one above to get started.</p>
			{:else}
				<div class="space-y-2">
					{@render tree(null, 0)}
				</div>
			{/if}
		</div>
	</div>

	{#if movePicker}
		<div
			class="fixed inset-0 bg-black/40 flex items-center justify-center p-4 z-10"
			onclick={() => (movePicker = null)}
			onkeydown={(e) => e.key === 'Escape' && (movePicker = null)}
			role="presentation"
		>
			<div
				class="bg-white rounded-lg shadow-lg p-4 max-w-sm w-full space-y-2 max-h-[80vh] overflow-y-auto"
				onclick={(e) => e.stopPropagation()}
				role="presentation"
				data-testid="move-picker"
			>
				<h2 class="text-lg font-semibold text-gray-800 mb-2">Move to…</h2>
				<button
					onclick={() => pickDestination(null)}
					class="block w-full text-left px-3 py-2 rounded hover:bg-gray-100"
				>
					(root)
				</button>
				{#each eligibleFolders() as f}
					<button
						onclick={() => pickDestination(f.id)}
						class="block w-full text-left px-3 py-2 rounded hover:bg-gray-100"
					>
						{f.name}
					</button>
				{/each}
				<button
					onclick={() => (movePicker = null)}
					class="block w-full text-center px-3 py-2 mt-2 text-gray-500 hover:text-gray-700"
				>
					Cancel
				</button>
			</div>
		</div>
	{/if}
{/if}
