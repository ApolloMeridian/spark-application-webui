import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright-core";

const base = process.env.QA_BASE_URL;
if (!base || !process.env.QA_CORE_COOKIE)
  throw new Error("Missing local QA origin/session");
const browser = await chromium.launch({
  executablePath:
    process.env.QA_BROWSER ||
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  headless: true,
});
await mkdir(".qa", { recursive: true });
try {
  for (const width of [1440, 1024]) {
    const context = await browser.newContext({
      viewport: { width, height: 1000 },
    });
    const [name, value] = process.env.QA_CORE_COOKIE.split("=");
    await context.addCookies([{ name, value, url: base }]);
    const page = await context.newPage();
    await page.route("**/config/config.js", (route) =>
      route.fulfill({
        contentType: "application/javascript",
        body: 'window.__SPARK_CONTROL_CENTER_CONFIG__={dataMode:"api",api:{baseUrl:"/api"},auth:{mode:"local"},cluster:{name:"kubernetes",namespaces:["spark"]}};',
      }),
    );
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.addInitScript(() =>
      localStorage.setItem("spark-console-locale", "en-US"),
    );
    await page.goto(`${base}/applications`, { waitUntil: "networkidle" });
    await page
      .getByRole("link", { name: "logs-regression", exact: true })
      .first()
      .waitFor();
    assert.equal(
      await page
        .getByRole("link", { name: "logs-regression", exact: true })
        .count(),
      2,
    );
    await page.getByText("Archived", { exact: false }).waitFor();
    await page.screenshot({
      path: `.qa/application-instances-${width}.png`,
      fullPage: true,
    });
    const rows = page.locator("tbody tr");
    const archived = rows.filter({ hasText: "Archived" });
    assert.equal(await archived.getByLabel("kill", { exact: true }).count(), 0);
    assert.equal(
      await archived.getByLabel("delete", { exact: true }).count(),
      0,
    );
    await archived
      .getByRole("link", { name: "logs-regression", exact: true })
      .click();
    await page.getByRole("heading").getByText("Historical snapshot", { exact: true }).waitFor();
    assert(!page.url().includes("/logs-regression"), "detail did not use UID");
    assert.equal(
      await page
        .getByRole("button", { name: "Kill application", exact: true })
        .count(),
      0,
    );
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "page overflow",
    );
    assert.deepEqual(errors, []);
    await context.close();
  }
  console.log(
    "PASS: archived/current same-name instances, canonical UID details and disabled archived mutations at 1440/1024.",
  );
} finally {
  await browser.close();
}
