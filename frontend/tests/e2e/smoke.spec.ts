import { test, expect } from '@playwright/test'

// Smoke test: boot the binary against tests/e2e/fixtures/cadence.yaml,
// log in with the configured read-only key, and verify the dashboard
// renders the configured check. Also exercises the SPA fallback served
// by the Go binary for unknown client-side routes.
test.describe('cadence dashboard', () => {
  test('gates on API key and renders the configured check', async ({ page }) => {
    await page.goto('/')

    // API key gate is the first thing an operator sees.
    const keyInput = page.getByPlaceholder('X-Api-Key')
    await expect(keyInput).toBeVisible()

    await keyInput.fill('e2e-key')
    await page.getByRole('button', { name: 'Continue' }).click()

    // Dashboard header + the row from the fixture config.
    await expect(page.getByRole('heading', { name: 'cadence' })).toBeVisible()
    await expect(page.getByText('1 checks')).toBeVisible()
    await expect(page.getByText('Smoke check')).toBeVisible()
    await expect(page.getByText('smoke-check')).toBeVisible()

    // Cron schedule rendered as a code chip.
    await expect(page.getByText('*/5 * * * *')).toBeVisible()

    // The freshly-declared check has never been pinged → status "New".
    await expect(page.getByText('New', { exact: true })).toBeVisible()
  })

  test('SPA fallback serves index.html for unknown client routes', async ({ request }) => {
    // The Go spaFallback handler is what we care about here: any path the
    // server doesn't recognize should return index.html (status 200, HTML
    // body) so TanStack Router can resolve the route client-side. We do
    // this via raw HTTP — what the router subsequently renders is a
    // client-side concern already covered by the vitest suite.
    const res = await request.get('/some-unknown-spa-route')
    expect(res.status()).toBe(200)
    expect(res.headers()['content-type']).toMatch(/text\/html/)
    expect(await res.text()).toContain('<div id="root">')
  })

  test('healthz responds 200', async ({ request }) => {
    const res = await request.get('/healthz')
    expect(res.status()).toBe(200)
    expect(await res.text()).toBe('ok')
  })
})
