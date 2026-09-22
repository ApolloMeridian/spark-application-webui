import { chromium } from 'playwright-core';
import { mkdir } from 'node:fs/promises';

const baseUrl = process.argv[2] || process.env.QA_URL || 'http://localhost:5173';
const output = process.env.QA_OUTPUT || '.qa';
await mkdir(output, { recursive: true });

const browser = await chromium.launch({
  executablePath: 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  headless: true,
});

const errors = [];
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 });
page.on('console', (event) => { if (event.type() === 'error') errors.push(event.text()); });
page.on('pageerror', (error) => errors.push(error.message));
await page.addInitScript(() => localStorage.setItem('spark-console-locale', 'en-US'));

await page.goto(`${baseUrl}/login`, { waitUntil: 'networkidle' });
await page.screenshot({ path: `${output}/login-1440.png`, fullPage: true });
await page.getByRole('button', { name: 'Sign in' }).click();
await page.waitForURL('**/overview');
await page.locator('.stat-card').first().waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/overview-1440.png`, fullPage: true });

await page.getByRole('menu').getByText('Spark Applications', { exact: true }).click();
await page.waitForURL('**/applications');
await page.getByText('parquet-to-iceberg', { exact: true }).waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/applications-1440.png`, fullPage: true });

await page.getByText('parquet-to-iceberg', { exact: true }).click();
await page.waitForURL('**/applications/spark-prod/parquet-to-iceberg');
await page.getByRole('tab', { name: 'Resources' }).click();
await page.locator('.ant-tabs-tabpane-active .ant-card-head-title', { hasText: 'Historical usage' }).waitFor({ state: 'visible' });
await page.locator('.tab-stack .drawer-loading').waitFor({ state: 'hidden' });
await page.screenshot({ path: `${output}/detail-1440.png`, fullPage: true });

await page.getByRole('tab', { name: /Executors/ }).click();
await page.getByRole('button', { name: 'View persisted Executor logs' }).first().click();
await page.locator('.ant-drawer-content-wrapper').waitFor({ state: 'visible' });
await page.waitForTimeout(500);
await page.screenshot({ path: `${output}/executor-logs-1440.png`, fullPage: true });
await page.locator('.ant-drawer-close').click();

await page.getByRole('button', { name: /demo-user/ }).click();
await page.getByText('admin', { exact: true }).click();
await page.locator('.ant-dropdown').waitFor({ state: 'hidden' });
await page.getByRole('button', { name: 'Kill application' }).click();
await page.locator('.ant-modal-content').waitFor({ state: 'visible' });
await page.getByPlaceholder('Reason (optional)').fill('Visual QA validation');
await page.waitForTimeout(250);
await page.screenshot({ path: `${output}/kill-modal-1440.png`, fullPage: true });
await page.getByRole('button', { name: 'Kill application' }).last().click();
await page.getByText('FAILED', { exact: true }).first().waitFor({ state: 'visible' });
await page.getByRole('button', { name: 'Delete record' }).click();
await page.locator('.ant-modal-content').waitFor({ state: 'visible' });
await page.getByPlaceholder('Reason (optional)').fill('Visual QA terminal cleanup');
await page.screenshot({ path: `${output}/delete-modal-1440.png`, fullPage: true });
await page.getByRole('button', { name: 'Delete application' }).click();
await page.waitForURL('**/applications');
await page.getByRole('menu').getByText('Operation Audit', { exact: true }).click();
await page.waitForURL('**/audit');
await page.getByText('Visual QA validation', { exact: true }).waitFor({ state: 'visible' });
await page.getByText('Visual QA terminal cleanup', { exact: true }).waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/audit-1440.png`, fullPage: true });

await page.getByRole('menu').getByText('Submit Spark Application', { exact: true }).click();
await page.waitForURL('**/submit');
await page.getByRole('button', { name: 'Submit to cluster' }).waitFor({ state: 'visible' });
await page.locator('.ant-message-notice').last().waitFor({ state: 'hidden' });
await page.screenshot({ path: `${output}/submit-1440.png`, fullPage: true });

const desktopMetrics = await page.evaluate(() => ({
  viewportWidth: document.documentElement.clientWidth,
  scrollWidth: document.documentElement.scrollWidth,
  bodyHeight: document.body.scrollHeight,
}));

await page.setViewportSize({ width: 1024, height: 768 });
await page.goto(`${baseUrl}/overview`);
await page.locator('.stat-card').first().waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/overview-1024.png`, fullPage: true });
const laptopMetrics = await page.evaluate(() => ({ viewportWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }));

console.log(JSON.stringify({ baseUrl, errors, desktopMetrics, laptopMetrics }, null, 2));
await browser.close();
