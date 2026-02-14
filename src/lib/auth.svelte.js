import { apiGet, apiPost, apiPut } from './api.js';

let user = $state(null);
let loading = $state(true);

export function getAuth() {
	return {
		get user() { return user; },
		get loading() { return loading; },
		get isLoggedIn() { return user !== null; },
	};
}

export async function checkAuth() {
	loading = true;
	try {
		user = await apiGet('/api/me');
	} catch {
		user = null;
	} finally {
		loading = false;
	}
}

export async function login(username, password) {
	user = await apiPost('/api/login', { username, password });
}

export async function register(username, email, password) {
	user = await apiPost('/api/register', { username, email: email || undefined, password });
}

export async function logout() {
	await apiPost('/api/logout', {});
	user = null;
}

export async function changeEmail(email) {
	user = await apiPut('/api/me/email', { email: email || null });
}

export async function changePassword(currentPassword, newPassword) {
	await apiPut('/api/me/password', {
		current_password: currentPassword,
		new_password: newPassword,
	});
}
