import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
	testDir: './tests',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 2 : 0,
	workers: process.env.CI ? 1 : undefined,
	reporter: [['html', { open: 'never', host: '0.0.0.0', port: 9323 }]],
	use: {
		baseURL: 'http://localhost:5174',
		trace: 'on-first-retry',
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'] },
		},
	],
	webServer: [
		{
			command: 'go run . server --config test/logger4life_test.conf',
			url: 'http://localhost:4001/api/hello',
		},
		{
			command: 'API_PORT=4001 npm run dev -- --port 5174',
			url: 'http://localhost:5174',
		},
	],
});
