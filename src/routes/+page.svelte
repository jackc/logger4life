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

<h1>{message}</h1>
