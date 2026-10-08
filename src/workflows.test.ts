import { describe, expect, it, vi, afterEach } from "vitest";
import { newWorkflow, validateDAG, workflowRequest } from "./workflows";
import { operationsService } from "./service";

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});
describe("workflow graph validation", () => {
  it("supports independent branches and all-parent dependencies", () => {
    const workflow = newWorkflow("project", "spark");
    const root = workflow.nodes[0];
    const child = { ...root, id: "second", dependsOn: [root.id] };
    expect(validateDAG([root, child])).toBeUndefined();
    expect(root.nameSuffix).toBe(true);
    expect(workflow.strategy).toBe("serial_wait");
  });
  it("rejects cycles and missing task references", () => {
    const root = newWorkflow("project", "spark").nodes[0];
    expect(validateDAG([{ ...root, dependsOn: [root.id] }])).toMatch(/cycle/);
    expect(validateDAG([{ ...root, dependsOn: ["missing"] }])).toMatch(
      /missing/,
    );
    expect(validateDAG([root, root])).toMatch(/unique/);
  });
});
describe("workflow API adapter", () => {
  it("uses authenticated same-origin requests", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue({ ok: true, json: async () => ({ id: "project" }) });
    vi.stubGlobal("fetch", fetcher);
    await workflowRequest("/projects", "POST", { name: "Example" });
    expect(fetcher).toHaveBeenCalledWith(
      "/api/v1/projects",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: '{"name":"Example"}',
      }),
    );
  });
  it("does not treat permission errors as successful saves", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 403,
        json: async () => ({
          message: "administrator permission is required",
        }),
      }),
    );
    await expect(workflowRequest("/projects", "POST", {})).rejects.toThrow(
      "administrator permission",
    );
  });
});
describe("template immutable version operations", () => {
  it("switches the current pointer and never reuses deleted version numbers", async () => {
    const first = await operationsService.saveTemplate({
      name: "Versioned",
      namespace: "spark-prod",
      manifest:
        "apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: versioned\nspec:\n  image: first\n",
      parameters: [],
    });
    const second = await operationsService.saveTemplate({
      ...first,
      manifest: first.manifest.replace("first", "second"),
    });
    expect(second.version).toBe(2);
    const restored = await operationsService.templateVersionAction(
      first.id,
      1,
      false,
    );
    expect(restored.version).toBe(1);
    expect(restored.versions).toHaveLength(2);
    expect(restored.manifest).toContain("first");
    await operationsService.templateVersionAction(first.id, 2, true);
    const current = await operationsService.getTemplate(first.id);
    expect(current.versions?.map((x) => x.version)).toEqual([1]);
    await expect(
      operationsService.templateVersionAction(first.id, 1, true),
    ).rejects.toThrow("Current");
    const edited = await operationsService.saveTemplate({
      ...current,
      manifest: second.manifest,
    });
    expect(edited.version).toBe(3);
    expect(edited.versions?.map((x) => x.version)).toEqual([1, 3]);
  });
});
