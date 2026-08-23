import { defineConfig, devices } from '@playwright/test';

// The browser suite runs its own backend and Vite, on this worktree's reserved
// test ports, against this worktree's PostgreSQL cluster. The fallbacks apply
// only when Playwright is started outside the development environment.
const backendPort = process.env.TEST_BACKEND_PORT ?? '4001';
const vitePort = process.env.TEST_VITE_PORT ?? '5174';
const backendURL = process.env.TEST_BACKEND_URL ?? `http://localhost:${backendPort}`;
const baseURL = process.env.TEST_VITE_URL ?? `http://localhost:${vitePort}`;
const databaseURL =
	process.env.TEST_DATABASE_URL ?? 'postgres://postgres:postgres@localhost:5432/logger4life_test';

export default defineConfig({
	testDir: './tests',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 2 : 0,
	workers: process.env.CI ? 1 : undefined,
	reporter: [
		[
			'html',
			{
				open: 'never',
				host: '127.0.0.1',
				port: Number(process.env.PLAYWRIGHT_REPORT_PORT ?? 9323)
			}
		]
	],
	use: {
		baseURL,
		trace: 'on-first-retry'
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'] }
		}
	],
	webServer: [
		{
			command: 'go run . server',
			env: {
				DATABASE_URL: databaseURL,
				BIND_ADDRESS: '127.0.0.1',
				PORT: backendPort,
				ALLOW_REGISTRATION: 'true',
				WEBAUTHN_RP_ID: 'localhost',
				WEBAUTHN_ORIGIN: baseURL
			},
			url: `${backendURL}/api/hello`
		},
		{
			command: 'npm run dev',
			env: {
				VITE_PORT: vitePort,
				BACKEND_URL: backendURL
			},
			url: baseURL
		}
	]
});
