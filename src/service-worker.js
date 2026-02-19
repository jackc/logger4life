/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />

import { build, files, version } from '$service-worker';

const CACHE_NAME = `logger4life-${version}`;
const ASSETS = [...build, ...files];

self.addEventListener('install', (event) => {
	event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.addAll(ASSETS)));
});

self.addEventListener('activate', (event) => {
	event.waitUntil(
		caches.keys().then((keys) =>
			Promise.all(keys.filter((key) => key !== CACHE_NAME).map((key) => caches.delete(key)))
		)
	);
});

self.addEventListener('fetch', (event) => {
	const url = new URL(event.request.url);

	// Skip API requests — always go to network
	if (url.pathname.startsWith('/api/')) {
		return;
	}

	// Navigation requests: network-first, fall back to cached index.html
	if (event.request.mode === 'navigate') {
		event.respondWith(fetch(event.request).catch(() => caches.match('/index.html')));
		return;
	}

	// Built assets (hashed filenames): cache-first
	if (ASSETS.includes(url.pathname)) {
		event.respondWith(caches.match(event.request).then((cached) => cached || fetch(event.request)));
		return;
	}

	// Everything else: network-first
	event.respondWith(fetch(event.request).catch(() => caches.match(event.request)));
});
