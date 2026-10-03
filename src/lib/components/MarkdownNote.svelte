<script>
	import { renderNote } from '$lib/markdown.js';
	let { note = '', collapsible = false } = $props();
	let expanded = $state(false);
	const long = $derived(note.length > 500 || note.split('\n').length > 8);
</script>

<div class="note-content" class:collapsed={collapsible && long && !expanded}>
	{@html renderNote(note)}
</div>
{#if collapsible && long}
	<button type="button" class="text-blue-600 hover:underline text-sm mt-2" aria-expanded={expanded} onclick={() => expanded = !expanded}>
		{expanded ? 'Show less' : 'Show more'}
	</button>
{/if}

<style>
	.note-content { overflow-wrap: anywhere; font-size: .875rem; line-height: 1.6; }
	.collapsed { max-height: 10rem; overflow: hidden; }
	.note-content :global(p), .note-content :global(ul), .note-content :global(ol), .note-content :global(pre), .note-content :global(blockquote) { margin: .5rem 0; }
	.note-content :global(h1), .note-content :global(h2), .note-content :global(h3), .note-content :global(h4), .note-content :global(h5), .note-content :global(h6) { font-weight: 700; margin: .75rem 0 .25rem; }
	.note-content :global(h1) { font-size: 1.35rem; }
	.note-content :global(h2) { font-size: 1.15rem; }
	.note-content :global(ul) { list-style: disc; padding-left: 1.5rem; }
	.note-content :global(ol) { list-style: decimal; padding-left: 1.5rem; }
	.note-content :global(a) { color: #2563eb; text-decoration: underline; }
	.note-content :global(blockquote) { border-left: 3px solid #d1d5db; padding-left: .75rem; color: #4b5563; }
	.note-content :global(code) { background: #f3f4f6; padding: .1rem .25rem; border-radius: .2rem; }
	.note-content :global(pre) { background: #f3f4f6; padding: .75rem; overflow-x: auto; border-radius: .25rem; }
	.note-content :global(pre code) { padding: 0; }
	.note-content :global(table) { display: block; overflow-x: auto; border-collapse: collapse; }
	.note-content :global(th), .note-content :global(td) { border: 1px solid #d1d5db; padding: .25rem .5rem; }
</style>
