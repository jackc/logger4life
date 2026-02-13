<script>
	import { getAuth } from '$lib/auth.svelte.js';
	import { goto } from '$app/navigation';

	const auth = getAuth();

	$effect(() => {
		if (!auth.loading && !auth.isLoggedIn) {
			goto('/login');
		}
	});
</script>

{#if auth.loading}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<p class="text-gray-500">Loading...</p>
	</div>
{:else if auth.isLoggedIn}
	<div class="min-h-screen bg-gray-100 flex items-center justify-center">
		<div class="bg-white rounded-lg shadow-lg p-8 w-full max-w-sm">
			<h1 class="text-2xl font-bold text-gray-800 mb-6 text-center">My Account</h1>
			<dl class="space-y-3">
				<div>
					<dt class="text-sm font-medium text-gray-500">Username</dt>
					<dd class="text-gray-900">{auth.user.username}</dd>
				</div>
				{#if auth.user.email}
					<div>
						<dt class="text-sm font-medium text-gray-500">Email</dt>
						<dd class="text-gray-900">{auth.user.email}</dd>
					</div>
				{/if}
			</dl>
		</div>
	</div>
{/if}
