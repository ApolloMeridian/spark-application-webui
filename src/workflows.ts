import { apiUrl, runtimeConfig } from "./runtimeConfig";
import type { ManifestPreview, TemplateParameter } from "./types";

export type ExecutionStrategy = "parallel" | "serial_wait" | "serial_discard";
export interface Tenant {
  permissions?: import('./permissions').TenantPermissions;
  id: string;
  name: string;
  createdAt: string;
}
export interface Project {
  id: string;
  tenantId: string;
  name: string;
  description: string;
  createdBy: string;
  createdAt: string;
}
export interface WorkflowNode {
  id: string;
  name: string;
  namespace: string;
  manifest: string;
  templateId?: string;
  templateVersion?: number;
  parameters: TemplateParameter[];
  values: Record<string, string>;
  dependsOn: string[];
  strategy: ExecutionStrategy;
  nameSuffix?: boolean;
  retries: number;
  timeoutMinutes: number;
  x: number;
  y: number;
}
export interface Workflow {
  id: string;
  projectId: string;
  tenantId: string;
  name: string;
  description: string;
  version: number;
  enabled: boolean;
  strategy: ExecutionStrategy;
  timeZone: string;
  maxParallel: number;
  timeoutMinutes: number;
  nodes: WorkflowNode[];
  createdBy: string;
  updatedAt: string;
}
export interface WorkflowVersion {
  version: number;
  snapshot: Workflow;
  createdBy: string;
  createdAt: string;
}
export interface TaskRun {
  id: string;
  runId: string;
  projectId: string;
  nodeId: string;
  name: string;
  status: string;
  attempt: number;
  applicationName: string;
  namespace: string;
  manifest: string;
  startedAt?: string;
  finishedAt?: string;
  nextAttemptAt?: string;
  message: string;
}
export interface WorkflowRun {
  id: string;
  projectId: string;
  workflowId: string;
  name: string;
  version: number;
  status: string;
  trigger: string;
  backfillId?: string;
  scheduledAt: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  createdBy: string;
  message: string;
  snapshot: Workflow;
  tasks: TaskRun[];
}
export interface WorkflowSchedule {
  id: string;
  projectId: string;
  workflowId: string;
  name: string;
  cron: string;
  timeZone: string;
  enabled: boolean;
  misfire: "skip" | "fire_once" | "catch_up";
  nextRunAt: string;
  startsAt?: string;
  endsAt?: string;
  createdBy: string;
}

export async function workflowRequest<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const response = await fetch(apiUrl(`/v1${path}`), {
    method,
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(runtimeConfig.api.requestTimeoutMs),
  });
  if (!response.ok) {
    const error = (await response.json().catch(() => ({}))) as {
      message?: string;
    };
    throw new Error(error.message || `HTTP ${response.status}`);
  }
  return response.json() as Promise<T>;
}
export interface BackfillRequest {
  id: string;
  workflowId: string;
  workflowName: string;
  version: number;
  cron: string;
  timeZone: string;
  startsAt: string;
  nextAt: string;
  endsAt: string;
  direction: "forward" | "backward";
  failurePolicy: "stop" | "continue";
  status: string;
  createdCount: number;
  error: string;
  createdAt: string;
}
const projectPath = (project: string) =>
  `/projects/${encodeURIComponent(project)}`;
const flowPath = (project: string, flow: string) =>
  `${projectPath(project)}/workflows/${encodeURIComponent(flow)}`;
export const workflowService = {
  backfills: (project: string) =>
    workflowRequest<BackfillRequest[]>(`${projectPath(project)}/backfills`),
  cancelBackfill: (project: string, id: string) =>
    workflowRequest(
      `${projectPath(project)}/backfills/${encodeURIComponent(id)}/cancel`,
      "POST",
      {},
    ),
  tenants: () => workflowRequest<Tenant[]>("/tenants"),
  saveTenant: (tenant: Partial<Tenant>) =>
    workflowRequest<Tenant>(
      tenant.id ? `/tenants/${encodeURIComponent(tenant.id)}` : "/tenants",
      tenant.id ? "PATCH" : "POST",
      tenant,
    ),
  projects: () => workflowRequest<Project[]>("/projects"),
  saveProject: (project: Partial<Project>) =>
    workflowRequest<Project>(
      project.id ? projectPath(project.id) : "/projects",
      project.id ? "PATCH" : "POST",
      project,
    ),
  deleteProject: (project: string) =>
    workflowRequest(projectPath(project), "DELETE"),
  workflows: (project: string) =>
    workflowRequest<Workflow[]>(`${projectPath(project)}/workflows`),
  saveWorkflow: (workflow: Workflow) =>
    workflowRequest<Workflow>(
      workflow.id
        ? flowPath(workflow.projectId, workflow.id)
        : `${projectPath(workflow.projectId)}/workflows`,
      workflow.id ? "PATCH" : "POST",
      workflow,
    ),
  deleteWorkflow: (project: string, flow: string) =>
    workflowRequest(flowPath(project, flow), "DELETE"),
  versions: (project: string, flow: string) =>
    workflowRequest<WorkflowVersion[]>(`${flowPath(project, flow)}/versions`),
  versionAction: (
    project: string,
    flow: string,
    version: number,
    remove: boolean,
  ) =>
    workflowRequest<Workflow>(
      `${flowPath(project, flow)}/versions/${version}${remove ? "" : "/restore"}`,
      remove ? "DELETE" : "POST",
      {},
    ),
  trigger: (
    project: string,
    flow: string,
    values: Record<string, Record<string, string>> = {},
  ) =>
    workflowRequest<WorkflowRun>(`${flowPath(project, flow)}/trigger`, "POST", {
      values,
    }),
  dryRun: (project: string, flow: string) =>
    workflowRequest<ManifestPreview[]>(
      `${flowPath(project, flow)}/dry-run`,
      "POST",
      {},
    ),
  backfill: (
    project: string,
    flow: string,
    body: {
      cron: string;
      timeZone: string;
      from: string;
      to: string;
      confirm: boolean;
      direction?: "forward" | "backward";
      failurePolicy?: "stop" | "continue";
    },
  ) =>
    workflowRequest<string[]>(
      `${flowPath(project, flow)}/backfill`,
      "POST",
      body,
    ),
  schedules: (project: string) =>
    workflowRequest<WorkflowSchedule[]>(`${projectPath(project)}/schedules`),
  saveSchedule: (schedule: Partial<WorkflowSchedule>) =>
    workflowRequest<WorkflowSchedule>(
      `${projectPath(schedule.projectId!)}/schedules${schedule.id ? `/${encodeURIComponent(schedule.id)}` : ""}`,
      schedule.id ? "PATCH" : "POST",
      schedule,
    ),
  deleteSchedule: (project: string, key: string) =>
    workflowRequest(
      `${projectPath(project)}/schedules/${encodeURIComponent(key)}`,
      "DELETE",
    ),
  cronPreview: (
    cron: string,
    timeZone: string,
    window: { startsAt?: string; endsAt?: string } = {},
  ) =>
    workflowRequest<string[]>("/workflows/cron-preview", "POST", {
      cron,
      timeZone,
      count: 10,
      ...window,
    }),
  runs: (project: string) =>
    workflowRequest<WorkflowRun[]>(`${projectPath(project)}/runs`),
  taskRuns: (project: string) =>
    workflowRequest<TaskRun[]>(`${projectPath(project)}/task-runs`),
  runAction: (project: string, run: string, action: string) =>
    workflowRequest(
      `${projectPath(project)}/runs/${encodeURIComponent(run)}/${action}`,
      "POST",
      {},
    ),
  templateVersionAction: (key: string, version: number, remove: boolean) =>
    workflowRequest(
      `/templates/${encodeURIComponent(key)}/versions/${version}${remove ? "" : "/restore"}`,
      remove ? "DELETE" : "POST",
      {},
    ),
};
export const exampleNodeYaml = `apiVersion: sparkoperator.k8s.io/v1beta2
kind: SparkApplication
metadata:
  name: spark-pi
spec:
  type: Scala
  mode: cluster
  image: apache/spark:3.5.3
  mainClass: org.apache.spark.examples.SparkPi
  mainApplicationFile: local:///opt/spark/examples/jars/spark-examples_2.12-3.5.3.jar
  sparkVersion: 3.5.3
  restartPolicy:
    type: Never
  driver:
    cores: 1
    memory: 512m
    serviceAccount: spark
  executor:
    instances: 1
    cores: 1
    memory: 512m
`;
export function newNode(namespace: string, index: number): WorkflowNode {
  return {
    id: `task-${crypto.randomUUID().slice(0, 8)}`,
    name: `Task ${index + 1}`,
    namespace,
    manifest: exampleNodeYaml,
    parameters: [],
    values: {},
    dependsOn: [],
    strategy: "parallel",
    nameSuffix: true,
    retries: 0,
    timeoutMinutes: 60,
    x: 30 + (index % 3) * 250,
    y: 35 + Math.floor(index / 3) * 150,
  };
}
export function newWorkflow(projectId: string, namespace: string): Workflow {
  return {
    id: "",
    projectId,
    tenantId: "",
    name: "",
    description: "",
    version: 0,
    enabled: true,
    strategy: "serial_wait",
    timeZone:
      runtimeConfig.timeZone ||
      Intl.DateTimeFormat().resolvedOptions().timeZone ||
      "UTC",
    maxParallel: 10,
    timeoutMinutes: 240,
    nodes: [newNode(namespace, 0)],
    createdBy: "",
    updatedAt: "",
  };
}
export function validateDAG(nodes: WorkflowNode[]): string | undefined {
  const keys = new Set(nodes.map((n) => n.id));
  if (!nodes.length || keys.size !== nodes.length)
    return "Task IDs must be unique; at least one task is required";
  const visiting = new Set<string>();
  const done = new Set<string>();
  const visit = (key: string): boolean => {
    if (visiting.has(key)) return false;
    if (done.has(key)) return true;
    visiting.add(key);
    const node = nodes.find((n) => n.id === key)!;
    for (const dep of node.dependsOn)
      if (!keys.has(dep) || !visit(dep)) return false;
    visiting.delete(key);
    done.add(key);
    return true;
  };
  return nodes.every((n) => visit(n.id))
    ? undefined
    : "Dependency cycle or missing task";
}
