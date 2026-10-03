<script>
	import MarkdownNote from './MarkdownNote.svelte';
	import { NOTE_LIMIT, noteLength } from '#lib/markdown.js';
	let { value = $bindable(''), open = $bindable(false), disabled = false } = $props();
	let preview = $state(false);
	const id = $props.id();
	const length = $derived(noteLength(value));
</script>

<div class="my-3">
	<button type="button" class="text-sm text-blue-600 hover:underline" aria-expanded={open} aria-controls={id} {disabled} onclick={() => { open = !open; preview = false; }}>
		{open ? 'Hide note' : value ? 'Edit note' : 'Add note'}
	</button>
	{#if open}
		<div {id} class="mt-2 rounded border border-gray-300 bg-white p-3">
			<div class="flex items-center gap-3 mb-2 text-sm">
				<span class="font-medium text-gray-700">Note</span>
				<button type="button" aria-pressed={!preview} class:font-semibold={!preview} class:underline={!preview} class="text-blue-600 disabled:opacity-50" {disabled} onclick={() => preview = false}>Write</button>
				<button type="button" aria-pressed={preview} class:font-semibold={preview} class:underline={preview} class="text-blue-600 disabled:opacity-50" {disabled} onclick={() => preview = true}>Preview</button>
			</div>
			{#if preview}
				<div class="min-h-28" aria-label="Note preview">
					{#if value}<MarkdownNote note={value} />{:else}<p class="text-sm text-gray-400">Nothing to preview yet.</p>{/if}
				</div>
			{:else}
				<textarea aria-label="Note" bind:value rows="5" {disabled} placeholder="Add a note… Markdown supported." class="w-full min-h-28 resize-y rounded border border-gray-300 p-2 text-sm"></textarea>
			{/if}
			<div class="text-xs text-gray-500 mt-1">Markdown supported: **bold**, *italic*, lists, links, and code.</div>
			{#if length >= NOTE_LIMIT - 1000}
				<p class:text-red-600={length > NOTE_LIMIT} class="text-xs mt-1" aria-live="polite">{length.toLocaleString()} / {NOTE_LIMIT.toLocaleString()} characters{length > NOTE_LIMIT ? ' — shorten your note to save.' : ''}</p>
			{/if}
		</div>
	{/if}
</div>
