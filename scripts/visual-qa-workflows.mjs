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
  tenantId: "default",
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
  tenantId: "default",
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
try {
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1050 },
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
      body: 'window.__SPARK_CONTROL_CENTER_CONFIG__ = {dataMode:"api",auth:{mode:"mock"},features:{workflows:true}};',
    }),
  );
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    let body;
    if (path.endsWith("/projects")) body = [project];
    else if (path.endsWith("/workflows"))
      body = route.request().method() === "GET" ? [flow] : flow;
    else if (path.endsWith("/schedules")) body = schedules;
    else if (path.endsWith("/task-runs")) body = run.tasks;
    else if (path.endsWith("/runs")) body = [run];
    else if (path.endsWith("/versions"))
      body = [4, 3, 2, 1].map((version) => ({
        version,
        snapshot: { ...flow, version },
        createdAt: at,
        createdBy: "platform-admin",
      }));
    else if (path.endsWith("/templates")) body = [];
    else if (path.endsWith("/cron-preview"))
      body = Array.from(
        { length: 10 },
        (_, i) => `2026-10-${String(i + 9).padStart(2, "0")}T02:00:00Z`,
      );
    else if (path.endsWith("/tenants"))
      body = [
        { id: "default", name: "default", createdAt: at },
        { id: "tenant-finance", name: "Finance", createdAt: at },
      ];
    else if (path.endsWith("/users"))
      body = [
        {
          id: "user-admin",
          username: "platform-admin",
          displayName: "Platform admin",
          email: "",
          role: "admin",
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
    .getByRole("button", { name: "Edit Daily lakehouse pipeline" })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.locator(".workflow-node").first().waitFor();
  await page.waitForTimeout(350);
  assert.equal(await dialog.locator(".workflow-node").count(), 4);
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
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.getByRole("tab", { name: "Schedules", exact: true }).click();
  await page.getByRole("button", { name: "New schedule" }).click();
  await dialog
    .getByRole("button", { name: "Preview next 10 executions" })
    .click();
  await dialog.locator(".workflow-cron-preview .ant-tag").first().waitFor();
  await page.waitForTimeout(350);
  await page.screenshot({
    path: `${output}/workflow-schedule-1440.png`,
    fullPage: true,
  });
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
  assert.deepEqual(failures, []);
  console.log(
    "PASS: English workflow DAG editor, Cron preview, project-scoped history and responsive 1440/1024px layouts.",
  );
} finally {
  await browser.close();
}
