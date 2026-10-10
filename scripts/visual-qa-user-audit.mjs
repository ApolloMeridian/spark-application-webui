import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const base = process.env.QA_BASE_URL;
const cookie = process.env.QA_CORE_COOKIE;
assert(base && cookie, "Use the isolated container smoke fixture");
const [name, ...parts] = cookie.split("=");
const browser = await chromium.launch({
  executablePath:
    process.env.QA_BROWSER ||
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  headless: true,
});
try {
  for (const width of [1920, 1024]) {
    const context = await browser.newContext({
      viewport: { width, height: 1000 },
    });
    await context.addCookies([{ name, value: parts.join("="), url: base }]);
    await context.addInitScript(() =>
      localStorage.setItem("spark-console-locale", "en-US"),
    );
    const page = await context.newPage();
    // Use the real core API, not the standalone image's mock defaults.
    await page.route("**/config/config.js", (route) =>
      route.fulfill({
        contentType: "application/javascript",
        body: 'window.__SPARK_CONTROL_CENTER_CONFIG__={dataMode:"api",api:{baseUrl:"/api"},auth:{mode:"local"},features:{audit:true,objectStorage:true}};',
      }),
    );
    const identity = await context.request.get(`${base}/api/v1/auth/me`);
    assert.equal(identity.status(), 200);
    assert.equal((await identity.json()).username, "qa-admin");
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto(`${base}/audit`, { waitUntil: "domcontentloaded" });
    await page
      .getByRole("cell", { name: "USER_TENANT_CHANGE", exact: true })
      .first()
      .waitFor();
    await page
      .getByRole("textbox", { name: "Search audit", exact: true })
      .fill("USER_TENANT_CHANGE");
    await page
      .getByRole("columnheader", { name: "Resource", exact: true })
      .waitFor();
    const rows = page.locator(".ant-table-tbody tr.ant-table-row");
    await page.waitForFunction(
      () =>
        document.querySelectorAll(".ant-table-tbody tr.ant-table-row")
          .length === 2,
    );
    assert.equal(await rows.count(), 2);
    assert((await rows.first().textContent()).includes("qa-viewer"));
    assert(
      (await rows.first().textContent()).includes(
        "Viewer storage users (viewer)",
      ),
    );
    assert((await rows.first().textContent()).includes("(default)"));
    assert.equal(
      await page
        .getByRole("cell", { name: "CONFIG_PATCH", exact: true })
        .count(),
      0,
    );
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    );
    await page.waitForFunction(
      () =>
        !document.querySelector(".ant-spin-spinning, .ant-spin-blur") &&
        [...document.querySelectorAll(".ant-spin-container")].every(
          (element) =>
            Number(getComputedStyle(element).opacity) >= 0.99 &&
            Number(getComputedStyle(element, "::after").opacity) <= 0.01,
        ),
    );
    await page.screenshot({
      path: `.qa/user-tenant-audit-${width}.png`,
      fullPage: true,
    });
    assert.deepEqual(errors, []);
    await context.close();
  }
  console.log(
    "PASS: real PostgreSQL user-tenant audit, explicit username/tenant transition, semantic action, English UI and no page overflow.",
  );
} finally {
  await browser.close();
}
