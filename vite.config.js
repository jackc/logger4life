import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { compression } from 'vite-plugin-compression2';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit(), compression({ algorithms: ['gzip', 'brotliCompress'] })],
	server: {
		host: '0.0.0.0',
		proxy: {
			'/api': {
				target: `http://localhost:${process.env.API_PORT || '4000'}`,
				changeOrigin: true
			}
		}
	}
});
