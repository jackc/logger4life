export async function apiPost(url, body) {
	const res = await fetch(url, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
	const data = await res.json();
	if (!res.ok) {
		throw new Error(data.error || 'Request failed');
	}
	return data;
}

export async function apiPut(url, body) {
	const res = await fetch(url, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
	const data = await res.json();
	if (!res.ok) {
		throw new Error(data.error || 'Request failed');
	}
	return data;
}

export async function apiDelete(url) {
	const res = await fetch(url, {
		method: 'DELETE',
	});
	if (!res.ok) {
		const data = await res.json();
		throw new Error(data.error || 'Request failed');
	}
}

export async function apiGet(url) {
	const res = await fetch(url);
	if (!res.ok) {
		if (res.status === 401) return null;
		throw new Error('Request failed');
	}
	return res.json();
}
