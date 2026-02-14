import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
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
