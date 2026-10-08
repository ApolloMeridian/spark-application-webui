import type { OperationAudit } from "./types";

export function operationColor(operation: string): string {
  if (/DELETE|REMOVE/.test(operation)) return "red";
  if (/KILL|CANCEL|TIMEOUT|BLOCKED/.test(operation)) return "orange";
  if (/PAUSE|CATCHUP_LIMIT/.test(operation)) return "gold";
  if (/RESTORE|RERUN|RETRY|RESUME/.test(operation)) return "purple";
  if (/TRIGGER|DISPATCH|SUBMIT|BACKFILL/.test(operation)) return "blue";
  if (/SAVE|CREATE|POST/.test(operation)) return "cyan";
  if (/PATCH|UPDATE/.test(operation)) return "geekblue";
  if (/FINISHED|ACKNOWLEDGE|RESOLVE/.test(operation)) return "green";
  return "default";
}
export type AuditFilters = {
  query: string;
  operator?: string;
  operation?: string;
  result?: string;
};
export function filterAudit(rows: OperationAudit[], filters: AuditFilters) {
  const query = filters.query.trim().toLowerCase();
  return rows.filter(
    (row) =>
      (!filters.operator || row.operator === filters.operator) &&
      (!filters.operation || row.operation === filters.operation) &&
      (!filters.result || row.result === filters.result) &&
      [
        row.applicationName,
        row.namespace,
        row.operator,
        row.operation,
        row.result,
        row.reason || "",
        row.message,
      ].some((v) => v.toLowerCase().includes(query)),
  );
}
