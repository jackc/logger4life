import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	kit: {
		adapter: adapter({
			pages: 'build/assets',
			assets: 'build/assets',
			fallback: 'index.html'
		})
	}
};

export default config;
