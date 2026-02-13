<script>
	let message = $state('Loading...');

	async function fetchHello() {
		try {
			const res = await fetch('/api/hello');
			const data = await res.json();
			message = data.message;
		} catch (err) {
			message = 'Error: ' + err.message;
		}
	}

	$effect(() => {
		fetchHello();
	});
</script>

<div class="min-h-screen bg-gray-100 flex items-center justify-center">
	<div class="bg-white rounded-lg shadow-lg p-8 text-center">
		<h1 class="text-4xl font-bold text-blue-600">{message}</h1>
	</div>
</div>
