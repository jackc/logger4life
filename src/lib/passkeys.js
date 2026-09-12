import { startRegistration, startAuthentication, browserSupportsWebAuthn } from '@simplewebauthn/browser';
import { apiPost, apiGet, apiPut, apiDelete } from './api.js';

export function isWebAuthnSupported() {
	return browserSupportsWebAuthn();
}

async function runPasskeyPrompt(prompt, options) {
	try {
		return await prompt({ optionsJSON: options });
	} catch (error) {
		const name = error.cause?.name ?? error.name;
		let message = 'Could not use your passkey. Please try again or use another sign-in method.';
		if (error.code === 'ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED' || name === 'InvalidStateError') {
			message = 'This passkey is already registered to your account. Try a different passkey.';
		} else if (error.code === 'ERROR_CEREMONY_ABORTED' || name === 'AbortError') {
			message = 'The passkey request was canceled. Please try again.';
		} else if (name === 'NotAllowedError') {
			// Browsers use this for cancellation, timeouts, and other refusals.
			message = 'The passkey request was canceled, timed out, or was not allowed. Please try again.';
		}
		throw new Error(message, { cause: error });
	}
}

export async function startPasskeyLogin() {
	const { options, challenge_id } = await apiPost('/api/passkey-login/begin', {});
	const credential = await runPasskeyPrompt(startAuthentication, options.publicKey);
	return apiPost('/api/passkey-login/finish', { challenge_id, credential });
}

export async function startPasskeyRegistration(description) {
	const { options, challenge_id } = await apiPost('/api/me/passkeys/register/begin', {});
	const credential = await runPasskeyPrompt(startRegistration, options.publicKey);
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
