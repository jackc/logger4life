import { startRegistration, startAuthentication, browserSupportsWebAuthn } from '@simplewebauthn/browser';
import { apiPost, apiGet, apiPut, apiDelete } from './api.js';

export function isWebAuthnSupported() {
	return browserSupportsWebAuthn();
}

export async function startPasskeyLogin() {
	const { options, challenge_id } = await apiPost('/api/passkey-login/begin', {});
	const credential = await startAuthentication({ optionsJSON: options.publicKey });
	return apiPost('/api/passkey-login/finish', { challenge_id, credential });
}

export async function startPasskeyRegistration(description) {
	const { options, challenge_id } = await apiPost('/api/me/passkeys/register/begin', {});
	const credential = await startRegistration({ optionsJSON: options.publicKey });
	return apiPost('/api/me/passkeys/register/finish', { challenge_id, credential, description });
}

export async function listPasskeys() {
	return apiGet('/api/me/passkeys');
}

export async function updatePasskeyDescription(id, description) {
	return apiPut(`/api/me/passkeys/${id}`, { description });
}

export async function deletePasskey(id) {
	return apiDelete(`/api/me/passkeys/${id}`);
}
