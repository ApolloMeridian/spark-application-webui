import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright-core";

const base = process.env.QA_BASE_URL || "http://127.0.0.1:5173";
await mkdir(".qa", { recursive: true });
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
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/config/config.js", (route) =>
      route.fulfill({
        contentType: "application/javascript",
        body: 'window.__SPARK_CONTROL_CENTER_CONFIG__={dataMode:"mock",auth:{mode:"mock"},features:{objectStorage:true}};',
      }),
    );
    await page.addInitScript(() => {
      if (!localStorage.getItem("spark-console-session"))
        localStorage.setItem(
          "spark-console-session",
          JSON.stringify({ username: "qa-admin", role: "admin" }),
        );
      localStorage.setItem("spark-console-locale", "en-US");
    });
    page.setDefaultTimeout(60000);
    await page.goto(`${base}/object-storage`, {
      waitUntil: "domcontentloaded",
      timeout: 60000,
    });
    await page
      .getByRole("heading", { name: "Object storage", exact: true })
      .waitFor();
    await page
      .getByRole("button", { name: "config.yaml", exact: true })
      .waitFor();
    await page
      .getByRole("button", { name: "Upload file", exact: true })
      .waitFor();
    await page.waitForFunction(
      () =>
        !document.querySelector('button[aria-label="Upload file"]')?.disabled,
    );
    await page.screenshot({
      path: `.qa/object-storage-${width}.png`,
      fullPage: true,
    });
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "page overflow",
    );
    await page.getByRole("button", { name: "large.log", exact: true }).click();
    await page.getByText(/Only the first .* is shown, read-only/).waitFor();
    assert(
      await page
        .getByRole("button", { name: "Save", exact: true })
        .isDisabled(),
    );
    await page.getByTestId("storage-code-editor").waitFor();
    await page
      .locator(".ant-drawer-open")
      .getByRole("button", { name: "Close", exact: true })
      .click();
    await page
      .getByRole("button", { name: "config.yaml", exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "File content", exact: true })
      .waitFor();
    const editor = page.getByRole("textbox", {
      name: "File content",
      exact: true,
    });
    await editor.focus();
    await editor.press("Control+a");
    await page.keyboard.insertText("spark:\n  executor:\n    instances: 4\n");
    await editor.press("Control+f");
    await page.locator(".find-widget.visible").waitFor();
    await page.keyboard.press("Escape");
    await editor.press("Control+h");
    await page.locator(".find-widget.visible").waitFor();
    await page.getByRole("checkbox", { name: /^Match Case/ }).waitFor();
    await page.getByRole("checkbox", { name: /^Match Whole Word/ }).waitFor();
    await page
      .getByRole("checkbox", { name: /^Use Regular Expression/ })
      .waitFor();
    await page.keyboard.press("Escape");
    await editor.press("Control+Home");
    await editor.press("Control+Alt+ArrowDown");
    assert(
      (await page.locator(".cursors-layer .cursor").count()) >= 2,
      "duplicate cursor",
    );
    await editor.press("Escape");
    await editor.press("Control+Home");
    await editor.press("Control+/");
    await editor.press("Control+/");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page
      .getByText("Operation completed", { exact: true })
      .first()
      .waitFor();
    await page.screenshot({
      path: `.qa/object-storage-editor-${width}.png`,
      fullPage: true,
    });
    await page
      .locator(".ant-drawer-open")
      .getByRole("button", { name: "Close", exact: true })
      .click();
    await page
      .getByRole("button", { name: "New text file", exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "File path", exact: true })
      .fill("team/analytics/new.json");
    const newEditor = page.getByRole("textbox", {
      name: "File content",
      exact: true,
    });
    await newEditor.focus();
    await newEditor.press("Control+a");
    await page.keyboard.type('{"broken":}');
    await page.locator('[role="status"]').filter({ hasText: /JSON/ }).waitFor();
    await newEditor.press("Control+a");
    await page.keyboard.type('{"name":"test","second":"test"}');
    await newEditor.press("Control+h");
    const findWidget = page.locator(".find-widget.visible");
    await findWidget
      .getByRole("textbox", { name: "Find", exact: true })
      .fill("test");
    await findWidget
      .getByRole("textbox", { name: "Replace", exact: true })
      .fill("changed");
    await findWidget.getByRole("button", { name: /^Replace \(/ }).click();
    await findWidget.getByRole("button", { name: /^Replace All/ }).click();
    await findWidget
      .getByRole("textbox", { name: "Find", exact: true })
      .fill("changed");
    await page.keyboard.press("Enter");
    await page.keyboard.press("Escape");
    await newEditor.press("Control+z");
    await newEditor.press("Control+y");
    await newEditor.press("Control+d");
    assert(
      (await page.locator(".cursors-layer .cursor").count()) >= 2,
      "Ctrl+D multi-selection",
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page
      .getByText("Operation completed", { exact: true })
      .first()
      .waitFor();
    await page.evaluate(() => {
      const fixture = JSON.parse(localStorage.getItem("spark-storage-fixture"));
      const content =
        fixture["demo/spark-artifacts/team/analytics/new.json"].content;
      if (content !== '{"name":"changed","second":"changed"}')
        throw new Error(
          `replace one/all did not save expected content: ${JSON.stringify(content)}`,
        );
    });
    await page
      .locator(".ant-drawer-open")
      .getByRole("button", { name: "Close", exact: true })
      .click();
    const download = page.waitForEvent("download");
    await page
      .getByRole("row")
      .filter({
        has: page.getByRole("button", { name: "new.json", exact: true }),
      })
      .getByRole("button", { name: "Download", exact: true })
      .click();
    await download;
    await page
      .getByRole("button", { name: "New text file", exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "File path", exact: true })
      .fill("team/analytics/validate.js");
    const jsEditor = page.getByRole("textbox", {
      name: "File content",
      exact: true,
    });
    await jsEditor.focus();
    await page.keyboard.type("const = ;");
    await page.getByTestId("storage-format-problems").waitFor();
    await jsEditor.press("Control+a");
    await page.keyboard.type("const answer = 42;");
    await page
      .getByTestId("storage-format-problems")
      .waitFor({ state: "hidden" });
    await page
      .locator(".ant-drawer-open")
      .getByRole("button", { name: "Close", exact: true })
      .click();
    const configRow = page.getByRole("row").filter({
      has: page.getByRole("button", { name: "config.yaml", exact: true }),
    });
    await configRow.getByRole("button", { name: "Copy", exact: true }).click();
    await page
      .getByRole("textbox", { name: "Destination key", exact: true })
      .fill("team/analytics/config-copy.yaml");
    await page
      .locator(".ant-modal-content")
      .getByRole("button", { name: "OK", exact: true })
      .click();
    await page
      .getByRole("button", { name: "config-copy.yaml", exact: true })
      .waitFor();
    await page
      .getByRole("button", { name: "Upload file", exact: true })
      .first()
      .click();
    await page.getByLabel("Choose upload file").setInputFiles({
      name: "upload-test.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("local QA upload"),
    });
    await page
      .locator(".ant-modal-content")
      .getByRole("button", { name: "OK", exact: true })
      .click();
    await page
      .getByRole("button", { name: "upload-test.txt", exact: true })
      .waitFor();
    await page.getByRole("button", { name: "scripts/", exact: true }).click();
    await page.getByRole("button", { name: "job.py", exact: true }).waitFor();
    await page.getByText("spark-artifacts", { exact: true }).click();
    await page
      .getByRole("button", { name: "config.yaml", exact: true })
      .waitFor();
    await page
      .getByRole("tab", { name: "Prefix permissions", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Add or update grant", exact: true })
      .waitFor();
    await page.screenshot({
      path: `.qa/object-storage-permissions-${width}.png`,
      fullPage: true,
    });
    await page
      .getByRole("tab", { name: "Storage operations", exact: true })
      .click();
    await page.getByRole("cell", { name: "EDIT", exact: true }).waitFor();
    assert.equal(
      await page.getByRole("cell", { name: "DOWNLOAD", exact: true }).count(),
      1,
    );
    await page.evaluate(() =>
      localStorage.setItem(
        "spark-console-session",
        JSON.stringify({ username: "qa-viewer", role: "viewer" }),
      ),
    );
    await page.reload();
    await page
      .getByRole("button", { name: "config.yaml", exact: true })
      .waitFor();
    assert(
      await page
        .getByRole("button", { name: "Upload file", exact: true })
        .isDisabled(),
    );
    assert.equal(
      await page
        .getByRole("tab", { name: "Prefix permissions", exact: true })
        .count(),
      0,
    );
    assert(
      await page
        .getByRole("button", { name: "Delete", exact: true })
        .first()
        .isDisabled(),
    );
    await page
      .getByRole("button", { name: "config.yaml", exact: true })
      .click();
    assert(
      await page
        .getByRole("button", { name: "Replace · Ctrl+H", exact: true })
        .isDisabled(),
    );
    assert(
      await page
        .getByRole("button", { name: "Save", exact: true })
        .isDisabled(),
    );
    assert.deepEqual(errors, []);
    await context.close();
  }
  const disabled = await browser.newPage();
  await disabled.route("**/config/config.js", (route) =>
    route.fulfill({
      contentType: "application/javascript",
      body: 'window.__SPARK_CONTROL_CENTER_CONFIG__={dataMode:"mock",auth:{mode:"mock"},features:{objectStorage:false}};',
    }),
  );
  await disabled.addInitScript(() => {
    localStorage.setItem(
      "spark-console-session",
      JSON.stringify({ username: "qa-admin", role: "admin" }),
    );
    localStorage.setItem("spark-console-locale", "en-US");
  });
  await disabled.goto(`${base}/object-storage`);
  await disabled.waitForURL("**/overview");
  assert.equal(
    await disabled
      .getByRole("menuitem", { name: "Object storage", exact: true })
      .count(),
    0,
  );
  console.log(
    "PASS: English 1440/1024 storage UI, Monaco editor/search/replace, text creation/diagnostics, bounded large-file preview, copy/upload/download audits, viewer grant restrictions, disabled navigation and no page overflow/errors.",
  );
} finally {
  await browser.close();
}
