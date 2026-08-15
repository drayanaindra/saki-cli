import { defineConfig } from 'vitest/config'

// `e2e/` holds PLAYWRIGHT specs, not vitest ones — they call test.describe() from
// @playwright/test and fail with "Playwright Test did not expect test.describe() to be called here"
// if vitest picks them up. Unit tests live beside their source under src/.
export default defineConfig({
  test: {
    include: ['src/**/*.test.ts'],
    exclude: ['node_modules/**', 'dist/**', 'e2e/**'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.ts'],
      exclude: ['src/**/*.test.ts'],
    },
  },
})
