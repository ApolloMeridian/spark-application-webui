import { chromium } from 'playwright-core';
import { mkdir } from 'node:fs/promises';

const baseUrl = process.argv[2] || process.env.QA_URL || 'http://localhost:5173';
const output = '.qa';
await mkdir(output, { recursive: true });

const browser = await chromium.launch({
  executablePath: 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  headless: true,
});

const errors = [];
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 });
page.on('console', (event) => { if (event.type() === 'error') errors.push(event.text()); });
page.on('pageerror', (error) => errors.push(error.message));

await page.goto(`${baseUrl}/login`, { waitUntil: 'networkidle' });
await page.screenshot({ path: `${output}/login-1440.png`, fullPage: true });
await page.getByRole('button', { name: '登录控制台' }).click();
await page.waitForURL('**/overview');
await page.locator('.stat-card').first().waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/overview-1440.png`, fullPage: true });

await page.getByRole('menu').getByText('Spark 作业', { exact: true }).click();
await page.waitForURL('**/applications');
await page.getByText('parquet-to-iceberg', { exact: true }).waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/applications-1440.png`, fullPage: true });

await page.getByText('parquet-to-iceberg', { exact: true }).click();
await page.waitForURL('**/applications/spark-prod/parquet-to-iceberg');
await page.getByText('Historical usage · last 1 hour', { exact: true }).waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/detail-1440.png`, fullPage: true });

await page.getByRole('button', { name: /jeremy/ }).click();
await page.getByText('admin', { exact: true }).click();
await page.locator('.ant-dropdown').waitFor({ state: 'hidden' });
await page.getByRole('button', { name: '终止作业' }).click();
await page.locator('.ant-modal-content').waitFor({ state: 'visible' });
await page.getByPlaceholder('操作原因（可选）').fill('Visual QA validation');
await page.waitForTimeout(250);
await page.screenshot({ path: `${output}/kill-modal-1440.png`, fullPage: true });
await page.getByRole('button', { name: '确认终止' }).click();
await page.waitForURL('**/applications');
await page.getByText('KILLED', { exact: true }).first().waitFor({ state: 'visible' });
await page.getByText('parquet-to-iceberg', { exact: true }).click();
await page.waitForURL('**/applications/spark-prod/parquet-to-iceberg');
await page.getByRole('button', { name: '删除记录' }).click();
await page.locator('.ant-modal-content').waitFor({ state: 'visible' });
await page.getByPlaceholder('操作原因（可选）').fill('Visual QA terminal cleanup');
await page.screenshot({ path: `${output}/delete-modal-1440.png`, fullPage: true });
await page.getByRole('button', { name: '确认删除' }).click();
await page.waitForURL('**/applications');
await page.getByRole('menu').getByText('操作审计', { exact: true }).click();
await page.waitForURL('**/audit');
await page.getByText('Visual QA validation', { exact: true }).waitFor({ state: 'visible' });
await page.getByText('Visual QA terminal cleanup', { exact: true }).waitFor({ state: 'visible' });
await page.screenshot({ path: `${output}/audit-1440.png`, fullPage: true });

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
