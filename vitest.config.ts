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
      // `json-summary` is load-bearing, not cosmetic: the pre-push coverage gate reads
      // coverage/coverage-summary.json. v8's DEFAULT reporters emit clover.xml +
      // coverage-final.json + html — none of which the gate's filename patterns match, so it
      // found no report and took its warn-and-allow path. The gate was inert until this line.
      reporter: ['text', 'json-summary', 'html'],
      include: ['src/**/*.ts'],
      exclude: ['src/**/*.test.ts'],
    },
  },
})
