import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TemplateParametersEditor } from "./components/TemplateParametersEditor";
import { WorkflowEditor } from "./components/WorkflowEditor";
import { newWorkflow } from "./workflows";

describe("workflow parameter definitions", () => {
  it("allows template default expressions without editing source constraints", async () => {
    const change = vi.fn();
    render(
      <TemplateParametersEditor
        en
        defaultsOnly
        parameters={[
          {
            name: "businessDate",
            type: "string",
            default: "old",
            required: true,
          },
        ]}
        onChange={change}
      />,
    );
    expect((screen.getByDisplayValue("businessDate") as HTMLInputElement).disabled).toBe(true);
    expect(
      screen.queryByRole("button", { name: "Add parameter" }),
    ).toBeNull();
    await userEvent.clear(screen.getByDisplayValue("old"));
    expect(change).toHaveBeenCalledWith([
      expect.objectContaining({
        name: "businessDate",
        type: "string",
        default: "",
        required: true,
      }),
    ]);
  });
  it("uses parameter defaults and preserves effective legacy values without a YAML values tab", async () => {
    const flow = newWorkflow("project", "spark");
    flow.nodes[0].parameters = [
      { name: "businessDate", type: "string", default: "$[yyyy-MM-dd-1]", required: false },
    ];
    flow.nodes[0].values = { businessDate: "$[yyyyMMdd-1]" };
    render(
      <WorkflowEditor
        en
        value={flow}
        onChange={vi.fn()}
        templates={[]}
        namespaces={["spark"]}
      />,
    );
    expect(
      screen.queryByRole("tab", { name: "Runtime values (YAML)" }),
    ).toBeNull();
    await userEvent.click(
      screen.getByRole("tab", { name: "Parameter definitions" }),
    );
    expect(screen.getByDisplayValue("$[yyyyMMdd-1]")).not.toBeNull();
  });
});
