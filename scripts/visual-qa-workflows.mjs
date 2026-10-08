import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright-core";
const output = process.env.QA_OUTPUT || ".qa";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({
  executablePath:
    process.env.QA_BROWSER ||
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  headless: true,
});
const at = "2026-10-08T02:00:00.123Z";
const project = {
  id: "project-data",
  tenantId: "tenant-platform",
  name: "Data platform",
  description: "Daily lakehouse processing and quality checks",
  createdBy: "platform-admin",
  createdAt: at,
};
const manifest =
  'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: daily-lakehouse\nspec:\n  type: Scala\n  mode: cluster\n  image: apache/spark:3.5.3\n  arguments: ["{{businessDate}}"]\n  restartPolicy:\n    type: Never\n  driver:\n    cores: 1\n    memory: 1g\n  executor:\n    instances: 4\n    cores: 2\n    memory: 4g\n';
const node = (id, name, x, y, dependsOn = []) => ({
  id,
  name,
  namespace: "spark-prod",
  manifest,
  parameters: [
    { name: "businessDate", type: "string", default: "$[yyyy-MM-dd-1]" },
  ],
  values: {},
  dependsOn,
  strategy: "parallel",
  nameSuffix: true,
  retries: 2,
  timeoutMinutes: 30,
  x,
  y,
});
const flow = {
  id: "flow-daily",
  projectId: project.id,
  tenantId: "tenant-platform",
  name: "Daily lakehouse pipeline",
  description: "Extract, transform and validate yesterday’s partition",
  version: 4,
  enabled: true,
  strategy: "serial_wait",
  timeZone: "Asia/Shanghai",
  maxParallel: 4,
  timeoutMinutes: 120,
  nodes: [
    node("extract", "Extract source", 25, 65),
    node("transform", "Transform lakehouse", 275, 25, ["extract"]),
    node("quality", "Quality checks", 275, 150, ["extract"]),
    node("publish", "Publish partition", 535, 65, ["transform", "quality"]),
  ],
  createdBy: "platform-admin",
  updatedAt: at,
};
const task = (n, i) => ({
  id: `task-${n.id}`,
  runId: "run-daily",
  projectId: project.id,
  nodeId: n.id,
  name: `${n.name}-20261008100000123-${i}`,
  status: i < 3 ? "succeeded" : "running",
  attempt: 1,
  applicationName: `daily-${n.id}-20261008020000123-${i}`,
  namespace: "spark-prod",
  manifest,
  startedAt: at,
  finishedAt: i < 3 ? "2026-10-08T02:03:12Z" : "",
  message: "",
});
const run = {
  id: "run-daily",
  projectId: project.id,
  workflowId: flow.id,
  name: "Daily-lakehouse-20261008100000123-a4b2c0d1",
  version: 4,
  status: "running",
  trigger: "schedule",
  scheduledAt: at,
  createdAt: at,
  startedAt: at,
  createdBy: "platform-admin",
  snapshot: flow,
  tasks: flow.nodes.map(task),
  message: "",
};
const schedules = [
  {
    id: "schedule-daily",
    projectId: project.id,
    workflowId: flow.id,
    name: "Daily at 10:00",
    cron: "0 10 * * *",
    timeZone: "Asia/Shanghai",
    enabled: true,
    misfire: "skip",
    nextRunAt: "2026-10-09T02:00:00Z",
    createdBy: "platform-admin",
  },
];
const failures = [];
const user = {
  id: "qa-admin",
  username: "platform-admin",
  displayName: "Platform admin",
  role: "admin",
  tenantId: "tenant-platform",
  namespaces: [],
  authSource: "local",
  disabled: false,
  createdAt: at,
  updatedAt: at,
};
let projectItems = [project];
let savedFlow;
let previewWindow;
let flowVersions = [4, 3, 2, 1];
let maxFlowVersion = 4;
let backfillBody;
const template = {
  id: "template-daily",
  name: "Daily Spark template",
  namespace: "spark-prod",
  manifest,
  parameters: flow.nodes[0].parameters,
  version: 2,
  disabled: false,
  createdBy: "platform-admin",
  createdAt: at,
  updatedAt: at,
  versions: [2, 1].map((version) => ({
    version,
    manifest,
    parameters: flow.nodes[0].parameters,
    createdBy: "platform-admin",
    createdAt: at,
  })),
};
let maxTemplateVersion = 2;
const audits = [
  "SUBMIT",
  "WORKFLOW_TRIGGER",
  "WORKFLOW_HISTORY_DELETE",
  "WORKFLOW_RESTORE",
  "WORKFLOW_PAUSE",
  "SCHEDULE_SAVE",
  "KILL",
].map((operation, i) => ({
  id: "audit-" + i,
  applicationName: flow.name,
  namespace: "spark-prod",
  operator: i % 2 ? "scheduler" : "platform-admin",
  operation,
  result: i === 6 ? "FAILED" : "SUCCESS",
  timestamp: at,
  reason: "Routine maintenance",
  message: "Workflow activity recorded",
}));
try {
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1050 },
    timezoneId: "Asia/Shanghai",
  });
  page.on("pageerror", (e) => failures.push(e.message));
  await page.addInitScript(() => {
    localStorage.setItem("spark-console-locale", "en-US");
    localStorage.setItem(
      "spark-console-session",
      JSON.stringify({ username: "platform-admin", role: "admin" }),
    );
  });
  await page.route("**/config/config.js", (route) =>
    route.fulfill({
      contentType: "application/javascript",
      body: 'window.__SPARK_CONTROL_CENTER_CONFIG__ = {dataMode:"api",auth:{mode:"local"},features:{workflows:true}};',
    }),
  );
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    let body;
    if (path.endsWith("/auth/me")) body = user;
    else if (path.endsWith("/audit")) body = audits;
    else if (
      path.endsWith("/projects/" + project.id) &&
      route.request().method() === "DELETE"
    ) {
      projectItems = [];
      body = { ok: true };
    } else if (path.endsWith("/projects")) body = projectItems;
    else if (
      path.endsWith("/workflows/" + flow.id) &&
      route.request().method() === "PATCH"
    ) {
      savedFlow = route.request().postDataJSON();
      savedFlow.version = ++maxFlowVersion;
      Object.assign(flow, savedFlow);
      flowVersions.unshift(flow.version);
      body = flow;
    } else if (/\/workflows\/[^/]+\/versions\/\d+\/restore$/.test(path)) {
      flow.version = Number(path.split("/").at(-2));
      body = flow;
    } else if (path.endsWith("/backfill")) {
      backfillBody = route.request().postDataJSON();
      body = Array.from({ length: 50 }, (_, i) =>
        new Date(backfillBody.direction==='backward' ? Date.parse(backfillBody.to)-i*1000 : Date.parse(backfillBody.from)+i*1000).toISOString(),
      );
    } else if (path.endsWith("/backfills")) body = [];
    else if (path === `/api/v1/templates/${template.id}`) {
      if (method === "PATCH") {
        Object.assign(template, route.request().postDataJSON());
        template.version = ++maxTemplateVersion;
        template.versions.unshift({
          version: template.version,
          manifest: template.manifest,
          parameters: template.parameters,
          createdBy: "platform-admin",
          createdAt: at,
        });
      }
      body = template;
    } else if (/\/templates\/[^/]+\/versions\/\d+\/restore$/.test(path)) {
      template.version = Number(path.split("/").at(-2));
      body = template;
    } else if (path.endsWith("/workflows"))
      body = route.request().method() === "GET" ? [flow] : flow;
    else if (path.endsWith("/schedules")) body = schedules;
    else if (path.endsWith("/task-runs")) body = run.tasks;
    else if (path.endsWith("/runs")) body = [run];
    else if (path.endsWith("/versions"))
      body = flowVersions.map((version) => ({
        version,
        snapshot: { ...flow, version },
        createdAt: at,
        createdBy: "platform-admin",
      }));
    else if (path.endsWith("/templates")) body = [template];
    else if (path.endsWith("/cron-preview")) {
      previewWindow = route.request().postDataJSON();
      body = Array.from({ length: 10 }, (_, i) =>
        previewWindow.cron === "*/5 * * * * *"
          ? new Date(
              Date.parse("2026-10-09T02:00:00Z") + i * 5000,
            ).toISOString()
          : `2026-10-${String(i + 9).padStart(2, "0")}T02:00:00Z`,
      );
      if (previewWindow.startsAt)
        body = body.filter((t) => t >= previewWindow.startsAt);
      if (previewWindow.endsAt)
        body = body.filter((t) => t <= previewWindow.endsAt);
      if (!body.length) {
        await route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({
            message:
              "invalid schedule range or Cron: no execution occurs within the valid future window",
          }),
        });
        return;
      }
    } else if (path.endsWith("/tenants"))
      body = [
        { id: "default", name: "default", createdAt: at },
        { id: "tenant-platform", name: "Data Platform", createdAt: at },
        { id: "tenant-finance", name: "Finance", createdAt: at },
      ];
    else if (path.endsWith("/users"))
      body = [
        user,
        {
          id: "user-admin",
          username: "lakehouse-data-quality-service-operator",
          displayName: "Lakehouse data quality operator",
          email: "data-quality@example.com",
          role: "viewer",
          tenantId: "default",
          namespaces: [],
          authSource: "local",
          disabled: false,
          createdAt: at,
          updatedAt: at,
        },
      ];
    else if (path.endsWith("/stream")) {
      await route.fulfill({
        contentType: "text/event-stream",
        body: ": heartbeat\n\n",
      });
      return;
    } else body = { ok: true };
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
  const url = process.env.QA_URL || "http://127.0.0.1:5173";
  await page.goto(`${url}/projects`, { waitUntil: "domcontentloaded" });
  await page
    .locator(".page-header-extra .ant-tag")
    .filter({ hasText: "Data Platform" })
    .waitFor();
  assert.equal(
    await page
      .locator(".page-header-extra")
      .getByText("tenant-platform", { exact: true })
      .count(),
    0,
  );
  await page.screenshot({
    path: `${output}/workflow-projects-1440.png`,
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Edit Daily lakehouse pipeline" })
    .click();
  const dialog = page.locator(".ant-modal:visible:not(.ant-zoom-leave)");
  await dialog.locator(".workflow-node").first().waitFor();
  assert.equal(
    await dialog
      .getByRole("tab", { name: "Runtime values (YAML)", exact: true })
      .count(),
    0,
  );
  await page.waitForTimeout(350);
  assert.equal(await dialog.locator(".workflow-node").count(), 4);
  // Remove a dependency, then rebuild it using a real pointer gesture.
  await dialog
    .getByRole("button", {
      name: "Select connection Extract source → Transform lakehouse",
      exact: true,
    })
    .click();
  await dialog
    .getByRole("button", { name: "Remove selected connection" })
    .click();
  const sourcePort = await dialog
    .getByRole("button", {
      name: "Connect from Extract source (right)",
      exact: true,
    })
    .boundingBox();
  const targetNode = await dialog
    .locator('[data-workflow-node="transform"]')
    .boundingBox();
  await page.mouse.move(
    sourcePort.x + sourcePort.width / 2,
    sourcePort.y + sourcePort.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(
    targetNode.x + targetNode.width / 2,
    targetNode.y + targetNode.height / 2,
    { steps: 12 },
  );
  await page.mouse.up();
  await dialog
    .getByRole("button", {
      name: "Select connection Extract source → Transform lakehouse",
      exact: true,
    })
    .waitFor();
  // A cycle must be rejected without corrupting the upstream form or saved DAG.
  const cyclePort = await dialog
    .getByRole("button", {
      name: "Connect from Transform lakehouse (left)",
      exact: true,
    })
    .boundingBox();
  const root = await dialog
    .locator('[data-workflow-node="extract"]')
    .boundingBox();
  await page.mouse.move(
    cyclePort.x + cyclePort.width / 2,
    cyclePort.y + cyclePort.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(root.x + root.width / 2, root.y + root.height / 2, {
    steps: 12,
  });
  await page.mouse.up();
  await dialog
    .getByText("This connection would create a dependency cycle.", {
      exact: true,
    })
    .waitFor();
  await dialog.locator(".ant-alert-close-icon").click();
  assert.equal(
    await dialog
      .locator(".ant-select-selection-item")
      .filter({ hasText: "Extract source" })
      .count(),
    1,
  );
  await page.screenshot({
    path: `${output}/workflow-editor-1440.png`,
    fullPage: true,
  });
  await page.setViewportSize({ width: 1024, height: 768 });
  assert(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
    ),
  );
  await page.screenshot({
    path: `${output}/workflow-editor-1024.png`,
    fullPage: true,
  });
  await dialog
    .getByRole("button", { name: "Save version", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  assert.deepEqual(
    savedFlow.nodes.find((n) => n.id === "transform").dependsOn,
    ["extract"],
  );
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.getByRole("button", { name: "Versions" }).click();
  let drawer = page.locator(".ant-drawer:visible");
  assert.equal(
    await drawer
      .getByRole("button", {
        name: `Delete version ${flow.version}`,
        exact: true,
      })
      .isDisabled(),
    true,
  );
  await drawer
    .locator(".ant-table-row")
    .filter({ hasText: "v1" })
    .getByRole("button", { name: "View", exact: true })
    .click();
  await dialog.getByText(/Viewing version v1/).waitFor();
  assert.equal(
    await dialog
      .getByRole("button", { name: "Delete this version", exact: true })
      .isDisabled(),
    true,
  );
  await dialog
    .getByRole("button", { name: "Cancel", exact: true })
    .count()
    .then(async (count) => {
      if (count)
        await dialog
          .getByRole("button", { name: "Cancel", exact: true })
          .click();
      else await dialog.locator(".ant-modal-close").click();
    });
  const firstVersion = drawer
    .locator(".ant-table-row")
    .filter({ hasText: "v1" });
  await firstVersion
    .getByRole("button", { name: "Restore", exact: true })
    .click();
  await page
    .locator(".ant-popconfirm:visible .ant-popconfirm-buttons .ant-btn-primary")
    .click();
  await drawer
    .locator('button[aria-label="Delete version 1"][disabled]')
    .waitFor();
  assert.equal(
    await drawer
      .getByRole("button", { name: "Delete version 1", exact: true })
      .isDisabled(),
    true,
  );
  assert.equal(flowVersions.length, 5);
  await firstVersion.getByRole("button", { name: "View", exact: true }).click();
  await dialog
    .getByRole("button", { name: "Edit as a new version", exact: true })
    .click();
  await dialog
    .getByRole("button", { name: "Save version", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(flow.version, 6);
  await page.getByRole("button", { name: "Backfill", exact: true }).click();
  await dialog
    .getByRole("textbox", { name: "Cron expression", exact: true })
    .fill("* * * * * *");
  const backfillInputs = dialog.locator(".ant-picker-input input");
  await backfillInputs.nth(0).fill("2026-10-07 00:00:00");
  await backfillInputs.nth(0).press("Enter");
  await backfillInputs.nth(1).fill("2026-10-08 00:00:00");
  await backfillInputs.nth(1).press("Enter");
  await dialog.getByText("Range", { exact: true }).click();
  await dialog
    .locator(".ant-form-item")
    .filter({ hasText: "Backfill order" })
    .locator(".ant-select-selector")
    .click();
  await page
    .locator(".ant-select-dropdown:visible")
    .getByText("Reverse · newest first", { exact: true })
    .click();
  await dialog
    .locator(".ant-form-item")
    .filter({ hasText: "On instance failure" })
    .locator(".ant-select-selector")
    .click();
  await page
    .locator(".ant-select-dropdown:visible")
    .getByText("Continue other occurrences", { exact: true })
    .click();
  await dialog
    .getByRole("button", { name: "Preview occurrences", exact: true })
    .click();
  await page.waitForFunction(
    () =>
      document.querySelectorAll(".workflow-cron-preview .ant-tag").length ===
      50,
  );
  assert.equal(backfillBody.direction, "backward");
  assert.equal(backfillBody.failurePolicy, "continue");
  await dialog.getByText('Backfill logical schedule times',{exact:true}).click();
  await dialog.locator('button.ant-btn-primary:not([disabled])').waitFor();
  await page.waitForTimeout(350);
  await page.locator('.ant-message').waitFor({state:'hidden'});
  await page.screenshot({
    path: `${output}/workflow-backfill-1440.png`,
    fullPage: true,
  });
  await dialog
    .getByRole("button", { name: "Confirm entire range", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(backfillBody.confirm, true);
  await page
    .getByRole("tab", { name: "Workflows & tasks", exact: true })
    .click();
  await page.getByRole("tab", { name: "Schedules", exact: true }).click();
  await page.getByRole("button", { name: "New schedule" }).click();
  await dialog
    .getByRole("combobox", { name: "Frequency", exact: true })
    .locator("..")
    .locator("..")
    .click();
  await page
    .locator(".ant-select-dropdown:visible .ant-select-item-option-content")
    .getByText("Every N seconds", { exact: true })
    .click();
  await dialog
    .getByRole("combobox", { name: "Interval", exact: true })
    .locator("..")
    .locator("..")
    .click();
  const intervalListId = await dialog
    .getByRole("combobox", { name: "Interval", exact: true })
    .getAttribute("aria-controls");
  const intervalDropdown = page
    .locator(".ant-select-dropdown")
    .filter({ has: page.locator(`[id="${intervalListId}"]`) });
  await intervalDropdown.locator(".rc-virtual-list-holder").evaluate((el) => {
    el.scrollTop = 320;
  });
  await intervalDropdown
    .locator(".ant-select-item-option-content")
    .getByText("15", { exact: true })
    .click();
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Cron expression", exact: true })
      .inputValue(),
    "*/15 * * * * *",
  );
  await dialog
    .getByRole("textbox", { name: "Cron expression", exact: true })
    .fill("*/5 * * * * *");
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Cron expression", exact: true })
      .inputValue(),
    "*/5 * * * * *",
  );
  await dialog
    .getByRole("textbox", { name: "Cron expression", exact: true })
    .fill("*/5 * * * * *");
  await dialog
    .getByRole("button", { name: "Preview next 10 executions" })
    .click();
  await dialog.locator(".workflow-cron-preview .ant-tag").first().waitFor();
  await page.waitForTimeout(350);
  await page.locator(".ant-message").waitFor({ state: "hidden" });
  await page.screenshot({
    path: `${output}/workflow-schedule-1440.png`,
    fullPage: true,
  });
  const dateInputs = dialog.locator(".ant-picker-input input");
  const setWindow = async (start, end) => {
    await dateInputs.nth(0).fill(start);
    await dateInputs.nth(0).press("Enter");
    await dateInputs.nth(1).fill(end);
    await dateInputs.nth(1).press("Enter");
    await dialog.getByText("Optional validity window", { exact: true }).click();
  };
  await dialog
    .getByRole("textbox", { name: "Cron expression", exact: true })
    .fill("0 0 10 * * *");
  await setWindow("2026-10-09 00:00:00", "2026-10-10 23:59:59");
  await dialog
    .getByRole("button", { name: "Preview next 10 executions" })
    .click();
  await page.waitForFunction(
    () =>
      Array.from(
        document.querySelectorAll(
          ".ant-modal:not(.ant-zoom-leave) .workflow-cron-preview .ant-tag",
        ),
      ).filter((el) => el.getClientRects().length).length === 2,
  );
  assert.equal(previewWindow.startsAt, "2026-10-08T16:00:00.000Z");
  assert.equal(previewWindow.endsAt, "2026-10-10T15:59:59.000Z");
  await setWindow("2026-10-09 00:00:00", "2026-10-09 00:01:00");
  await dialog
    .getByRole("button", { name: "Preview next 10 executions" })
    .click();
  await dialog
    .getByText(/Time range \/ Cron configuration is incorrect/)
    .waitFor();
  assert.equal(
    await dialog.locator(".workflow-cron-preview .ant-tag").count(),
    0,
  );
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await page
    .getByRole("tab", { name: "Execution history", exact: true })
    .click();
  await page.getByRole("button", { name: run.name, exact: true }).click();
  await page.locator(".ant-drawer .workflow-node").first().waitFor();
  await page.waitForTimeout(350);
  await page.screenshot({
    path: `${output}/workflow-history-1440.png`,
    fullPage: true,
  });
  await page.setViewportSize({ width: 1024, height: 768 });
  assert(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
    ),
  );
  await page.screenshot({
    path: `${output}/workflow-history-1024.png`,
    fullPage: true,
  });
  await page.locator(".ant-drawer-close:visible").click();
  await page.setViewportSize({width:1440,height:1050});
  await page.goto(`${url}/templates`);
  await page.getByRole("button", { name: "Versions" }).click();
  drawer = page.locator(".ant-drawer:visible");
  const templateV1 = drawer.locator(".ant-table-row").filter({ hasText: "v1" });
  await templateV1
    .getByRole("button", { name: "View YAML", exact: true })
    .click();
  await dialog.getByText(/Viewing version v1/).waitFor();
  assert.equal(
    await dialog
      .getByRole("button", { name: "Delete this version", exact: true })
      .isDisabled(),
    true,
  );
  await dialog.locator(".ant-modal-close").click();
  await templateV1
    .getByRole("button", { name: "Restore", exact: true })
    .click();
  await page
    .locator(".ant-popconfirm:visible .ant-popconfirm-buttons .ant-btn-primary")
    .click();
  await page.waitForTimeout(200);
  assert.equal(template.version, 1);
  assert.equal(template.versions.length, 2);
  await templateV1
    .getByRole("button", { name: "View YAML", exact: true })
    .click();
  await dialog
    .getByRole("button", { name: "Edit as a new version", exact: true })
    .click();
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(template.version, 3);
  await page.screenshot({
    path: `${output}/templates-1440.png`,
    fullPage: true,
  });
  await page.goto(`${url}/users`);
  await page
    .getByText("lakehouse-data-quality-service-operator", { exact: true })
    .waitFor();
  await page.locator(".ant-spin-spinning").waitFor({ state: "hidden" });
  await page.waitForTimeout(350);
  await page.screenshot({ path: `${output}/users-1440.png`, fullPage: true });
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.waitForTimeout(350);
  assert(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
    ),
  );
  await page.screenshot({ path: `${output}/users-1024.png`, fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto(`${url}/audit`);
  await page.locator(".audit-operation-tag").first().waitFor();
  await page.locator(".ant-spin-spinning").waitFor({ state: "hidden" });
  assert.equal(await page.locator(".audit-operation-tag").count(), 7);
  for (const tag of await page.locator(".audit-operation-tag").all()) {
    const bounds = await tag.boundingBox();
    const cell = await tag.locator("..").boundingBox();
    assert(bounds.x + bounds.width <= cell.x + cell.width);
  }
  await page.screenshot({ path: `${output}/audit-1440.png`, fullPage: true });
  const select = async (name, option) => {
    await page.getByRole("combobox", { name, exact: true }).click();
    await page
      .locator(".ant-select-dropdown:visible .ant-select-item-option-content")
      .getByText(option, { exact: true })
      .click();
  };
  await select("Operator", "platform-admin");
  await select("Operation", "KILL");
  await select("Result", "FAILED");
  assert.equal(
    await page.locator(".ant-table-tbody .ant-table-row").count(),
    1,
  );
  await page
    .getByRole("button", { name: "Reset filters", exact: true })
    .click();
  assert.equal(
    await page.locator(".ant-table-tbody .ant-table-row").count(),
    7,
  );
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.waitForTimeout(350);
  assert(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
    ),
  );
  await page.screenshot({ path: `${output}/audit-1024.png`, fullPage: true });
  await page.goto(`${url}/projects`);
  await page
    .getByRole("button", { name: "Delete project", exact: true })
    .click();
  await page.getByRole("button", { name: "OK", exact: true }).click();
  await page
    .getByText("Create or select a project to begin", { exact: true })
    .waitFor();
  await page.waitForTimeout(11000);
  assert.equal(
    await page
      .getByText("resource not found in this tenant", { exact: true })
      .count(),
    0,
  );
  assert.equal(
    await page.evaluate(() =>
      localStorage.getItem("spark-console-project-qa-admin-tenant-platform"),
    ),
    null,
  );
  assert.deepEqual(failures, []);
  console.log(
    "PASS: pointer DAG connections/form sync, tenant names, second-Cron, bounded/empty validity previews, audit filters, last-project deletion, English 1440/1024px layouts.",
  );
} finally {
  await browser.close();
}
