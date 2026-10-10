import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const base = process.env.QA_BASE_URL || "http://127.0.0.1:5173";
const minimal = {
  allNamespaces: true,
  namespaces: [],
  submit: false,
  templatesRead: false,
  templatesWrite: false,
  workflowsRead: false,
  workflowsWrite: false,
  kill: false,
  delete: false,
  alertsRead: false,
  alertsWrite: false,
  objectStorage: false,
};
const browser = await chromium.launch({
  executablePath:
    process.env.QA_BROWSER ||
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  headless: true,
});
try {
  for (const width of [1440, 1024]) {
    const page = await browser.newPage({ viewport: { width, height: 1000 } });
    let saved;
    const tenants = [
      {
        id: "admin",
        name: "admin",
        permissions: Object.fromEntries(
          Object.entries(minimal).map(([key, value]) => [
            key,
            typeof value === "boolean" ? true : value,
          ]),
        ),
      },
      { id: "viewer", name: "viewer", permissions: minimal },
      { id: "default", name: "default" },
    ];
    await page.route("**/config/config.js", (route) =>
      route.fulfill({
        contentType: "application/javascript",
        body: 'window.__SPARK_CONTROL_CENTER_CONFIG__={dataMode:"mock",auth:{mode:"mock"},features:{objectStorage:true,workflows:true}};',
      }),
    );
    await page.route("**/api/v1/tenants**", (route) => {
      if (route.request().method() === "PATCH") {
        saved = route.request().postDataJSON();
        return route.fulfill({ json: { ...saved, id: "viewer" } });
      }
      return route.fulfill({ json: tenants });
    });
    await page.route("**/api/v1/users**", (route) =>
      route.fulfill({
        json: [
          {
            id: "mock-admin",
            username: "platform-admin",
            displayName: "Platform Administrator",
            email: "",
            role: "admin",
            tenantId: "admin",
            namespaces: [],
            disabled: false,
            authSource: "mock",
            createdAt: "2026-10-01T00:00:00Z",
            updatedAt: "2026-10-01T00:00:00Z",
          },
        ],
      }),
    );
    await page.addInitScript(() => {
      localStorage.setItem("spark-console-locale", "en-US");
      if (!localStorage.getItem("spark-console-session"))
        localStorage.setItem(
          "spark-console-session",
          JSON.stringify({ username: "qa-admin", role: "admin" }),
        );
    });
    await page.goto(`${base}/users`);
    await page
      .getByRole("heading", { name: "User management", exact: true })
      .waitFor();
    const icons = await page
      .locator(".ant-menu-item svg[data-icon]")
      .evaluateAll((elements) =>
        elements.map((x) => x.getAttribute("data-icon")),
      );
    assert.equal(new Set(icons).size, icons.length, "duplicate module icons");
    await page
      .getByRole("button", { name: /Manage tenants|Tenant management/ })
      .click();
    const viewerRow = page
      .getByRole("row")
      .filter({ has: page.getByRole("cell", { name: "viewer", exact: true }) });
    await viewerRow.getByRole("button").click();
    const dialog = page.getByRole("dialog", { name: "Save tenant" });
    await dialog
      .getByText("Job templates (including Save as template)")
      .waitFor();
    await dialog.getByRole("switch").last().click();
    await page.screenshot({
      path: `.qa/tenant-permissions-${width}.png`,
      fullPage: true,
      animations: "disabled",
    });
    await dialog.getByRole("button", { name: "OK", exact: true }).click();
    assert.equal(saved.permissions.objectStorage, true);
    assert.equal(saved.permissions.workflowsWrite, false);
    await page.evaluate(
      (permissions) =>
        localStorage.setItem(
          "spark-console-session",
          JSON.stringify({
            username: "qa-viewer",
            role: "viewer",
            tenantId: "viewer",
            permissions,
          }),
        ),
      minimal,
    );
    await page.goto(`${base}/object-storage`);
    await page.waitForURL("**/overview");
    assert.equal(
      await page.getByRole("menuitem").count(),
      2,
      "minimum tenant must expose only overview and applications",
    );
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "horizontal overflow",
    );
    await page.close();
  }
  console.log(
    "PASS: unique module icons, tenant permission editor, persisted policy payload, minimum tenant route/menu guards at 1440/1024.",
  );
} finally {
  await browser.close();
}
