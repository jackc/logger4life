import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit()],
	server: {
		host: '0.0.0.0',
		proxy: {
			'/api': {
				target: 'http://localhost:4000',
				changeOrigin: true
			}
		}
	}
});
