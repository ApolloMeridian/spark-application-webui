export type ApplicationState =
  | 'RUNNING'
  | 'COMPLETED'
  | 'FAILED'
  | 'SUBMISSION_FAILED'
  | 'PENDING'
  | 'SUBMITTED'
  | 'FAILING'
  | 'UNKNOWN'
  | 'KILLED';

export type UserRole = 'viewer' | 'operator' | 'admin';
export type Locale = 'zh-CN' | 'en-US';

export interface ResourceAmount {
  cpu: number;
  memoryGiB: number;
}

export interface PodResource {
  request: ResourceAmount;
  current?: ResourceAmount;
  peak?: ResourceAmount;
}

export interface MetricPoint {
  time: string;
  cpu: number;
  memoryGiB: number;
}

export interface ExecutorPod {
  name: string;
  state: 'RUNNING' | 'PENDING' | 'SUCCEEDED' | 'FAILED' | 'TERMINATED';
  rawState?: string;
  resources: PodResource;
  node?: string;
  nodePool?: 'baseline' | 'autoscale';
  startedAt?: string;
  restarts: number;
}

export interface KubernetesEvent {
  id: string;
  type: 'Normal' | 'Warning';
  reason: string;
  message: string;
  source: string;
  timestamp: string;
  count: number;
}

export interface OperationAudit {
  id: string;
  applicationName: string;
  namespace: string;
  operator: string;
  operation: 'KILL';
  reason?: string;
  timestamp: string;
  result: 'SUCCESS' | 'FAILED';
  message: string;
}

export interface SparkApplication {
  id: string;
  name: string;
  namespace: string;
  cluster: string;
  owner: string;
  team: string;
  state: ApplicationState;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  image: string;
  sparkVersion: string;
  sparkApplicationId?: string;
  submissionId: string;
  driverPod: string;
  driverNode?: string;
  driverNodePool?: 'baseline' | 'autoscale';
  driver: PodResource;
  executors: ExecutorPod[];
  metrics: MetricPoint[];
  events: KubernetesEvent[];
  logs: string[];
  yaml: string;
  pendingReason?: string;
  errorMessage?: string;
}

export interface DashboardSummary {
  total: number;
  byState: Partial<Record<ApplicationState, number>>;
  requested: ResourceAmount;
  used: ResourceAmount;
  capacity: ResourceAmount;
  metricsAvailable: boolean;
  nodePools: { baseline: number; autoscale: number; pending: number };
}

export interface ApplicationFilters {
  keyword?: string;
  state?: ApplicationState;
  owner?: string;
  namespace?: string;
}

export interface SparkApplicationService {
  getSummary(filters?: ApplicationFilters): Promise<DashboardSummary>;
  listApplications(filters?: ApplicationFilters): Promise<SparkApplication[]>;
  getApplication(namespace: string, name: string): Promise<SparkApplication>;
  getAudit(): Promise<OperationAudit[]>;
  killApplication(namespace: string, name: string, operator: string, reason?: string): Promise<OperationAudit>;
  reset(): Promise<void>;
}
