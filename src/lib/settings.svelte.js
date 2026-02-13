import { apiGet } from './api.js';

let allowRegistration = $state(true);
let loaded = $state(false);

export function getSettings() {
	return {
		get allowRegistration() { return allowRegistration; },
		get loaded() { return loaded; },
	};
}

export async function loadSettings() {
	try {
		const data = await apiGet('/api/settings');
		allowRegistration = data.allow_registration;
	} catch {
		allowRegistration = true;
	} finally {
		loaded = true;
	}
}
