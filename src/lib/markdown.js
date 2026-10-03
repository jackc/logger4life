import MarkdownIt from 'markdown-it';

// Keep raw HTML inert and never load remote images from notes.
const markdown = new MarkdownIt({ html: false, breaks: true }).disable('image');
export function renderNote(note) {
	return markdown.render(note || '');
}
export const NOTE_LIMIT = 20000;
export const noteLength = (note) => Array.from(note || '').length;
