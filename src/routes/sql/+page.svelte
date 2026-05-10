<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { getAuth } from '$lib/auth.svelte.js';
	import { apiGet, apiPost, apiPut, apiDelete } from '$lib/api.js';
	import { EditorView, basicSetup } from 'codemirror';
	import { keymap } from '@codemirror/view';
	import { Prec } from '@codemirror/state';
	import { sql } from '@codemirror/lang-sql';

	const auth = getAuth();

	let editorContainer;
	let editorView;
	let queryText = $state('SELECT name, fields, shared_with FROM logs;');

	let result = $state(null);
	let error = $state('');
	let running = $state(false);

	let schema = $state(null);
	let schemaError = $state('');

	let savedQueries = $state([]);
	let loadedQueryId = $state(null);
	let saveBusy = $state(false);
	let saveError = $state('');

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) goto('/login');
	});

	$effect(() => {
		if (auth.isLoggedIn) {
			loadSchema();
			loadSavedQueries();
		}
	});

	function setEditorContent(text) {
		queryText = text;
		if (editorView) {
			editorView.dispatch({
				changes: { from: 0, to: editorView.state.doc.length, insert: text }
			});
		}
	}

	onMount(() => {
		editorView = new EditorView({
			parent: editorContainer,
			doc: queryText,
			extensions: [
				Prec.highest(
					keymap.of([{ key: 'Mod-Enter', run: () => { runQuery(); return true; } }])
				),
				basicSetup,
				sql(),
				EditorView.updateListener.of((u) => {
					if (u.docChanged) queryText = u.state.doc.toString();
				})
			]
		});
		return () => editorView?.destroy();
	});

	async function loadSchema() {
		try {
			schemaError = '';
			schema = await apiGet('/api/sql/schema');
		} catch (e) {
			schemaError = e.message;
		}
	}

	async function loadSavedQueries() {
		try {
			savedQueries = (await apiGet('/api/sql/saved')) || [];
		} catch (e) {
			// non-fatal
		}
	}

	async function runQuery() {
		if (running) return;
		running = true;
		error = '';
		try {
			result = await apiPost('/api/sql/execute', { query: queryText });
		} catch (e) {
			error = e.message;
			result = null;
		} finally {
			running = false;
		}
	}

	function loadSaved(q) {
		setEditorContent(q.query_text);
		loadedQueryId = q.id;
	}

	async function saveAsNew() {
		const name = prompt('Name for this query:');
		if (!name) return;
		saveBusy = true;
		saveError = '';
		try {
			const created = await apiPost('/api/sql/saved', { name, query_text: queryText });
			savedQueries = [...savedQueries, created].sort((a, b) =>
				a.name.toLowerCase().localeCompare(b.name.toLowerCase())
			);
			loadedQueryId = created.id;
		} catch (e) {
			saveError = e.message;
		} finally {
			saveBusy = false;
		}
	}

	async function updateLoaded() {
		const current = savedQueries.find((q) => q.id === loadedQueryId);
		if (!current) return;
		saveBusy = true;
		saveError = '';
		try {
			const updated = await apiPut(`/api/sql/saved/${current.id}`, {
				name: current.name,
				query_text: queryText
			});
			savedQueries = savedQueries.map((q) => (q.id === updated.id ? updated : q));
		} catch (e) {
			saveError = e.message;
		} finally {
			saveBusy = false;
		}
	}

	async function deleteLoaded() {
		const current = savedQueries.find((q) => q.id === loadedQueryId);
		if (!current) return;
		if (!confirm(`Delete saved query "${current.name}"?`)) return;
		saveBusy = true;
		saveError = '';
		try {
			await apiDelete(`/api/sql/saved/${current.id}`);
			savedQueries = savedQueries.filter((q) => q.id !== current.id);
			loadedQueryId = null;
		} catch (e) {
			saveError = e.message;
		} finally {
			saveBusy = false;
		}
	}
</script>

<div class="max-w-7xl mx-auto p-4 grid lg:grid-cols-[1fr_2fr] gap-4">
	<aside class="space-y-4">
		<section class="bg-white rounded-lg shadow p-4">
			<h2 class="font-bold mb-2">Schema</h2>
			{#if schemaError}
				<p class="text-red-600 text-sm">{schemaError}</p>
			{:else if !schema}
				<p class="text-gray-400 text-sm">Loading…</p>
			{:else}
				{#each schema.views as view}
					<details class="mb-3">
						<summary class="cursor-pointer font-mono text-sm font-semibold">{view.name}</summary>
						{#if view.comment}
							<p class="text-xs text-gray-600 mt-1 mb-2">{view.comment}</p>
						{/if}
						<table class="w-full text-xs border-t mt-1">
							<thead>
								<tr class="text-left text-gray-500">
									<th class="py-1 pr-2">Column</th>
									<th class="py-1 pr-2">Type</th>
									<th class="py-1">Description</th>
								</tr>
							</thead>
							<tbody>
								{#each view.columns as col}
									<tr class="border-t border-gray-100 align-top">
										<td class="py-1 pr-2 font-mono">{col.name}</td>
										<td class="py-1 pr-2 text-gray-600 font-mono">{col.data_type}</td>
										<td class="py-1 text-gray-600">{col.comment ?? ''}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</details>
				{/each}
			{/if}
		</section>

		<section class="bg-white rounded-lg shadow p-4">
			<h2 class="font-bold mb-2">Saved queries</h2>
			{#if savedQueries.length === 0}
				<p class="text-gray-400 text-sm">No saved queries yet.</p>
			{:else}
				<ul class="space-y-1">
					{#each savedQueries as q}
						<li>
							<button
								type="button"
								onclick={() => loadSaved(q)}
								class="w-full text-left text-sm py-1 px-2 rounded hover:bg-blue-50 {loadedQueryId ===
								q.id
									? 'bg-blue-100 font-semibold'
									: ''}">{q.name}</button
							>
						</li>
					{/each}
				</ul>
			{/if}
			<div class="flex gap-2 mt-3">
				<button
					type="button"
					onclick={saveAsNew}
					disabled={saveBusy || !queryText.trim()}
					class="text-sm bg-blue-600 text-white py-1 px-3 rounded hover:bg-blue-700 disabled:opacity-50"
					>Save as…</button
				>
				{#if loadedQueryId}
					<button
						type="button"
						onclick={updateLoaded}
						disabled={saveBusy}
						class="text-sm bg-gray-200 text-gray-800 py-1 px-3 rounded hover:bg-gray-300 disabled:opacity-50"
						>Update</button
					>
					<button
						type="button"
						onclick={deleteLoaded}
						disabled={saveBusy}
						class="text-sm text-red-600 hover:text-red-800 py-1 px-3 disabled:opacity-50">Delete</button
					>
				{/if}
			</div>
			{#if saveError}
				<p class="text-red-600 text-sm mt-2">{saveError}</p>
			{/if}
		</section>
	</aside>

	<main class="space-y-4">
		<section class="bg-white rounded-lg shadow p-4">
			<div class="flex items-center justify-between mb-2">
				<h2 class="font-bold">Query</h2>
				<button
					type="button"
					onclick={runQuery}
					disabled={running || !queryText.trim()}
					class="bg-blue-600 text-white py-1 px-4 rounded hover:bg-blue-700 disabled:opacity-50"
				>
					{running ? 'Running…' : 'Run (⌘⏎)'}
				</button>
			</div>
			<div bind:this={editorContainer} class="border rounded font-mono text-sm min-h-[140px]"></div>
		</section>

		<section class="bg-white rounded-lg shadow p-4">
			{#if error}
				<p class="text-red-600 whitespace-pre-wrap">{error}</p>
			{:else if !result}
				<p class="text-gray-400 text-sm">No results yet — write a query and click Run.</p>
			{:else}
				<div class="flex items-center gap-3 mb-2 text-sm text-gray-600">
					<span>{result.row_count} row{result.row_count === 1 ? '' : 's'}</span>
					<span>·</span>
					<span>{result.elapsed_ms} ms</span>
					{#if result.truncated}
						<span class="text-amber-600">· truncated to 1000 rows</span>
					{/if}
				</div>
				{#if result.columns.length === 0}
					<p class="text-gray-500 text-sm">Query returned no columns.</p>
				{:else}
					<div class="overflow-x-auto">
						<table class="text-sm border-collapse">
							<thead>
								<tr class="bg-gray-50">
									{#each result.columns as col}
										<th class="text-left p-2 border-b font-semibold">
											<div>{col.name}</div>
											<div class="text-xs text-gray-500 font-normal font-mono">{col.data_type}</div>
										</th>
									{/each}
								</tr>
							</thead>
							<tbody>
								{#each result.rows as row}
									<tr class="border-b">
										{#each row as cell}
											<td class="p-2 align-top font-mono">
												{#if cell === null}
													<span class="text-gray-400 italic">NULL</span>
												{:else}
													{cell}
												{/if}
											</td>
										{/each}
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{/if}
		</section>
	</main>
</div>
