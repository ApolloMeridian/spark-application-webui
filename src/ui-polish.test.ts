import { describe, it, expect } from "vitest";
import { filterAudit, operationColor } from "./audit";
import { buildCron, defaultCronSettings, readCron } from "./cron";
import { connectNodes, edgeGeometry, nodeAt } from "./workflowGraph";
import { newNode } from "./workflows";
import type { OperationAudit } from "./types";

describe("graph connection editing", () => {
  const a = { ...newNode("spark", 0), id: "a", x: 40, y: 40 };
  const b = { ...newNode("spark", 1), id: "b", x: 350, y: 40 };
  const c = { ...newNode("spark", 2), id: "c", x: 350, y: 220 };
  it("adds fan-out and fan-in to the same upstream dependency model", () => {
    let nodes = connectNodes([a, b, c], "a", "b").nodes;
    nodes = connectNodes(nodes, "a", "c").nodes;
    nodes = connectNodes(nodes, "b", "c").nodes;
    expect(nodes.find((n) => n.id === "c")?.dependsOn).toEqual(["a", "b"]);
    expect(connectNodes(nodes, "a", "c").error).toBe("duplicate");
    expect(connectNodes(nodes, "c", "a").error).toBe("cycle");
    expect(connectNodes(nodes, "a", "a").error).toBe("self");
    expect(connectNodes(nodes, "missing", "a").error).toBe("missing");
  });
  it("routes right, left, down and up from facing card borders", () => {
    expect(edgeGeometry(a, b).sourceSide).toBe("right");
    expect(edgeGeometry(b, a).sourceSide).toBe("left");
    expect(edgeGeometry(a, { ...b, x: a.x, y: 230 }).sourceSide).toBe("bottom");
    expect(edgeGeometry({ ...a, y: 230 }, { ...b, x: a.x }).sourceSide).toBe(
      "top",
    );
    expect(nodeAt([a, b, c], { x: 360, y: 230 })?.id).toBe("c");
    expect(nodeAt([a, b], { x: 0, y: 0 })).toBeUndefined();
  });
});
describe("point-and-click Cron", () => {
  it("round-trips each frequency and preserves a legacy five-field daily schedule", () => {
    for (const mode of [
      "seconds",
      "minutes",
      "hourly",
      "daily",
      "weekly",
      "monthly",
      "yearly",
    ] as const) {
      const input = { ...defaultCronSettings, mode };
      const expression = buildCron(input);
      expect(expression.split(" ")).toHaveLength(6);
      expect(buildCron(readCron(expression))).toBe(expression);
    }
    expect(buildCron(readCron("0 2 * * *"))).toBe("0 0 2 * * *");
  });
  it("preserves advanced raw expressions and allows multiple weekdays/dates", () => {
    expect(readCron("0 0 2 1-15 * MON-FRI").mode).toBe("custom");
    expect(
      buildCron({
        ...defaultCronSettings,
        mode: "weekly",
        weekdays: [5, 1, 3],
      }),
    ).toBe("0 0 2 * * 1,3,5");
    expect(
      buildCron({ ...defaultCronSettings, mode: "monthly", days: [15, 1] }),
    ).toBe("0 0 2 1,15 * *");
  });
});
describe("audit filters and semantic operation colors", () => {
  const row = (
    id: string,
    operator: string,
    operation: string,
    result: OperationAudit["result"],
  ): OperationAudit => ({
    id,
    operator,
    operation,
    result,
    namespace: "spark",
    applicationName: "workflow-test",
    reason: "maintenance",
    message: "done",
    timestamp: "2026-10-08T00:00:00Z",
  });
  const rows = [
    row("1", "alice", "WORKFLOW_HISTORY_DELETE", "SUCCESS"),
    row("2", "bob", "WORKFLOW_TRIGGER", "SUCCESS"),
    row("3", "alice", "KILL", "FAILED"),
  ];
  it("combines exact selections with keyword search and resets to all records", () => {
    expect(
      filterAudit(rows, {
        query: "WORKFLOW",
        operator: "alice",
        operation: "WORKFLOW_HISTORY_DELETE",
        result: "SUCCESS",
      }).map((r) => r.id),
    ).toEqual(["1"]);
    expect(
      filterAudit(rows, { query: "", result: "FAILED" }).map((r) => r.id),
    ).toEqual(["3"]);
    expect(filterAudit(rows, { query: " MAINTENANCE " })).toHaveLength(3);
    expect(filterAudit(rows, { query: "" })).toHaveLength(3);
  });
  it("distinguishes destructive, execution, restore, pause and configuration actions", () => {
    expect(operationColor("WORKFLOW_HISTORY_DELETE")).toBe("red");
    expect(operationColor("WORKFLOW_TRIGGER")).toBe("blue");
    expect(operationColor("WORKFLOW_RESTORE")).toBe("purple");
    expect(operationColor("WORKFLOW_PAUSE")).toBe("gold");
    expect(operationColor("SCHEDULE_SAVE")).toBe("cyan");
    expect(operationColor("SOMETHING_NEW")).toBe("default");
  });
});
