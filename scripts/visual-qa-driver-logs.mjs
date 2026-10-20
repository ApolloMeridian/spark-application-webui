import assert from "node:assert/strict";
import { chromium } from "playwright-core";
const base = process.env.QA_BASE_URL;
const cookie = process.env.QA_CORE_COOKIE;
assert(base && cookie);
const [name, ...parts] = cookie.split("=");
const browser = await chromium.launch({
  executablePath:
    process.env.QA_BROWSER ||
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  headless: true,
});
try {
  for (const width of [1440, 1024]) {
    const context = await browser.newContext({
      viewport: { width, height: 1000 },
    });
    await context.addCookies([{ name, value: parts.join("="), url: base }]);
    await context.addInitScript(() =>
      localStorage.setItem("spark-console-locale", "en-US"),
    );
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/config/config.js", (r) =>
      r.fulfill({
        contentType: "application/javascript",
        body: 'window.__SPARK_CONTROL_CENTER_CONFIG__={dataMode:"api",auth:{mode:"local"},api:{baseUrl:"/api"}};',
      }),
    );
    await page.goto(`${base}/applications/spark/logs-regression`, {
      waitUntil: "domcontentloaded",
    });
    await page.getByRole("tab", { name: "Driver logs", exact: true }).click();
    await page.getByText("Latest logs", { exact: true }).click();
    await page.getByText("Line range", { exact: true }).click();
    await page.getByRole("button", { name: /Query logs/ }).click();
    await page
      .locator(".log-viewer > div")
      .filter({ hasText: "driver-line-00001" })
      .waitFor();
    await page.getByRole("spinbutton", { name: "Start line" }).fill("6001");
    await page.getByRole("spinbutton", { name: "End line" }).fill("8000");
    await page.getByRole("button", { name: /Query logs/ }).click();
    await page
      .locator(".log-viewer > div")
      .filter({ hasText: "driver-line-06001" })
      .waitFor();
    await page.waitForFunction(
      () => !document.querySelector(".ant-spin-spinning,.ant-spin-blur"),
    );
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "page overflow",
    );
    await page.screenshot({
      path: `.qa/driver-log-range-${width}.png`,
      fullPage: true,
    });
    await page.getByRole("button", { name: "Next range", exact: true }).click();
    await page
      .locator(".log-viewer > div")
      .filter({ hasText: "driver-line-08001" })
      .waitFor();
    await page
      .getByRole("button", { name: "Previous range", exact: true })
      .click();
    await page
      .locator(".log-viewer > div")
      .filter({ hasText: "driver-line-06001" })
      .waitFor();
    assert.deepEqual(errors, []);
    await context.close();
  }
  console.log(
    "PASS: real API Driver line ranges beyond 5000, next/previous navigation and English 1440/1024 layout.",
  );
} finally {
  await browser.close();
}
