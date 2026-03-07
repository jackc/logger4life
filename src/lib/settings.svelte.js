import { apiGet } from './api.js';

let allowRegistration = $state(true);
let passkeysEnabled = $state(false);
let loaded = $state(false);

export function getSettings() {
	return {
		get allowRegistration() { return allowRegistration; },
		get passkeysEnabled() { return passkeysEnabled; },
		get loaded() { return loaded; },
	};
}

export async function loadSettings() {
	try {
		const data = await apiGet('/api/settings');
		allowRegistration = data.allow_registration;
		passkeysEnabled = data.passkeys_enabled;
	} catch {
		allowRegistration = true;
		passkeysEnabled = false;
	} finally {
		loaded = true;
	}
}
