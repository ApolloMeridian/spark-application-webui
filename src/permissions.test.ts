import { describe, expect, it } from "vitest";
import { hasPermission, minimalPermissions } from "./permissions";
import type { UserAccount } from "./types";

describe("tenant capabilities", () => {
  const user = {
    role: "viewer",
    disabled: false,
    permissions: { ...minimalPermissions },
  } as UserAccount;
  it("minimum tenant has no optional module access, even for administrators", () => {
    for (const key of [
      "submit",
      "kill",
      "delete",
      "templatesRead",
      "workflowsRead",
      "alertsRead",
      "objectStorage",
    ] as const) {
      expect(hasPermission(user, key)).toBe(false);
      expect(hasPermission({ ...user, role: "admin" }, key)).toBe(false);
    }
  });
  it("delegated write permission requires read access and an active account", () => {
    const delegated = {
      ...user,
      permissions: {
        ...minimalPermissions,
        workflowsRead: true,
        workflowsWrite: true,
      },
    };
    expect(hasPermission(delegated, "workflowsWrite")).toBe(true);
    expect(
      hasPermission({ ...delegated, disabled: true }, "workflowsWrite"),
    ).toBe(false);
    expect(
      hasPermission(
        {
          ...delegated,
          permissions: { ...delegated.permissions, workflowsRead: false },
        },
        "workflowsWrite",
      ),
    ).toBe(false);
  });
});
