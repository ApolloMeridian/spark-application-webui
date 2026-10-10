import type { UserAccount } from "./types";

export interface TenantPermissions {
  allNamespaces: boolean;
  namespaces: string[];
  submit: boolean;
  templatesRead: boolean;
  templatesWrite: boolean;
  workflowsRead: boolean;
  workflowsWrite: boolean;
  kill: boolean;
  delete: boolean;
  alertsRead: boolean;
  alertsWrite: boolean;
  objectStorage: boolean;
}
export type Capability = Exclude<
  keyof TenantPermissions,
  "allNamespaces" | "namespaces"
>;
export const minimalPermissions: TenantPermissions = {
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
export function hasPermission(
  user: UserAccount | null | undefined,
  capability: Capability,
): boolean {
  if (!user || user.disabled) return false;
  const p = user.permissions;
  if (!p)
    return (
      [
        "templatesRead",
        "workflowsRead",
        "alertsRead",
        "objectStorage",
      ].includes(capability) || user.role === "admin"
    );
  if (capability === "templatesWrite")
    return p.templatesRead && p.templatesWrite;
  if (capability === "workflowsWrite")
    return p.workflowsRead && p.workflowsWrite;
  if (capability === "alertsWrite") return p.alertsRead && p.alertsWrite;
  return Boolean(p[capability]);
}
