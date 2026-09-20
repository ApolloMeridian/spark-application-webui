import type { ApplicationFilters, DashboardSummary, OperationAudit, SparkApplication, SparkApplicationService } from './types';
import { seedApplications } from './mockData';
import { apiUrl, runtimeConfig } from './runtimeConfig';
import { sumResources } from './utils';

const APPS_KEY = 'spark-console-mock-applications';
const AUDIT_KEY = 'spark-console-mock-audit';
const wait = (ms = 220) => new Promise((resolve) => setTimeout(resolve, ms));

function cloneSeed() { return JSON.parse(JSON.stringify(seedApplications)) as SparkApplication[]; }
function readApps(): SparkApplication[] {
  const stored = localStorage.getItem(APPS_KEY);
  if (!stored) { const initial = cloneSeed(); localStorage.setItem(APPS_KEY, JSON.stringify(initial)); return initial; }
  return JSON.parse(stored);
}
function readAudit(): OperationAudit[] { return JSON.parse(localStorage.getItem(AUDIT_KEY) || '[]'); }
function matches(app: SparkApplication, filters: ApplicationFilters = {}) {
  const keyword = filters.keyword?.toLowerCase().trim();
  return (!keyword || [app.name, app.owner, app.team].some((value) => value.toLowerCase().includes(keyword)))
    && (!filters.state || app.state === filters.state)
    && (!filters.owner || app.owner === filters.owner)
    && (!filters.namespace || app.namespace === filters.namespace);
}

export class MockSparkApplicationService implements SparkApplicationService {
  async listApplications(filters: ApplicationFilters = {}) { await wait(); return readApps().filter((app) => matches(app, filters)); }
  async getApplication(namespace: string, name: string) {
    await wait(); const app = readApps().find((item) => item.namespace === namespace && item.name === name);
    if (!app) throw new Error('Application not found'); return app;
  }
  async getSummary(filters: ApplicationFilters = {}): Promise<DashboardSummary> {
    const apps = await this.listApplications(filters);
    const requested = { cpu: 0, memoryGiB: 0 }; const used = { cpu: 0, memoryGiB: 0 };
    const byState: DashboardSummary['byState'] = {}; let baseline = 0; let autoscale = 0; let pending = 0;
    apps.forEach((app) => {
      byState[app.state] = (byState[app.state] ?? 0) + 1;
      if (app.state !== 'RUNNING') return;
      const totals = sumResources(app); requested.cpu += totals.requested.cpu; requested.memoryGiB += totals.requested.memoryGiB;
      used.cpu += totals.used.cpu; used.memoryGiB += totals.used.memoryGiB;
      app.executors.filter((executor) => ['RUNNING', 'PENDING'].includes(executor.state)).forEach((executor) => { if (!executor.node) pending += 1; else if (executor.nodePool === 'baseline') baseline += 1; else autoscale += 1; });
    });
    const to = filters.to ? new Date(filters.to) : new Date();
    const from = filters.from ? new Date(filters.from) : new Date(to.getTime() - runtimeConfig.dashboard.defaultHistoryDays * 86400000);
    const submitted = apps.filter((app) => { const time = new Date(app.createdAt); return time >= from && time <= to; }).length;
    const failed = apps.filter((app) => ['FAILED', 'SUBMISSION_FAILED'].includes(app.state) && app.finishedAt && new Date(app.finishedAt) >= from && new Date(app.finishedAt) <= to).length;
    return { total: apps.length, byState, requested, used, capacity: { cpu: 288, memoryGiB: 2458 }, metricsAvailable: true, nodePools: { baseline, autoscale, pending }, history: { submitted, failed, from: from.toISOString(), to: to.toISOString() } };
  }
  async getAudit() { await wait(150); return readAudit().sort((a, b) => b.timestamp.localeCompare(a.timestamp)); }
  async submitApplication(namespace: string, yaml: string, operator: string) {
    await wait(450);
    if (!/^apiVersion:\s*sparkoperator\.k8s\.io\/v1beta2\s*$/m.test(yaml) || !/^kind:\s*SparkApplication\s*$/m.test(yaml)) throw new Error('YAML must be a sparkoperator.k8s.io/v1beta2 SparkApplication');
    const metadata = yaml.match(/^metadata:\s*\n((?:[ \t]+.*\n?)*)/m)?.[1] ?? '';
    const name = metadata.match(/^\s+name:\s*["']?([^\s"']+)["']?\s*$/m)?.[1];
    const manifestNamespace = metadata.match(/^\s+namespace:\s*["']?([^\s"']+)["']?\s*$/m)?.[1];
    if (!name) throw new Error('YAML metadata.name is required');
    if (manifestNamespace && manifestNamespace !== namespace) throw new Error('YAML metadata.namespace must match the selected namespace');
    const apps = readApps();
    if (apps.some((app) => app.namespace === namespace && app.name === name)) throw new Error('SparkApplication already exists');
    const now = new Date().toISOString();
    const app: SparkApplication = {
      ...cloneSeed()[0], id: `${namespace}/${name}`, name, namespace, cluster: runtimeConfig.cluster.name,
      state: 'SUBMITTED', createdAt: now, startedAt: undefined, finishedAt: undefined,
      sparkApplicationId: undefined, sparkUiAvailable: false, eventLogEnabled: false, submissionId: `mock-${Date.now()}`, driverPod: `${name}-driver`,
      driverNode: undefined, driverNodePool: undefined, executors: [], metrics: [], events: [], logs: [],
      yaml, pendingReason: undefined, errorMessage: undefined,
    };
    const audit: OperationAudit = { id: `op-${Date.now()}`, applicationName: name, namespace, operator, operation: 'SUBMIT', timestamp: now, result: 'SUCCESS', message: 'SparkApplication created by Kubernetes API (mock)' };
    apps.unshift(app); localStorage.setItem(APPS_KEY, JSON.stringify(apps)); localStorage.setItem(AUDIT_KEY, JSON.stringify([audit, ...readAudit()]));
    return app;
  }
  async killApplication(namespace: string, name: string, operator: string, reason?: string) {
    await wait(550); const apps = readApps(); const app = apps.find((item) => item.namespace === namespace && item.name === name);
    if (!app) throw new Error('Application not found');
    if (app.state !== 'RUNNING') throw new Error('Only RUNNING applications can be killed');
    const audit: OperationAudit = { id: `op-${Date.now()}`, applicationName: name, namespace, operator, operation: 'KILL', reason, timestamp: new Date().toISOString(), result: 'SUCCESS', message: 'Driver pod force deleted and executor pods cleaned; SparkApplication retained (mock)' };
    app.state = 'FAILED'; app.finishedAt = new Date().toISOString(); app.errorMessage = 'Driver pod was force deleted by the operator'; app.sparkUiAvailable = false; app.executors = []; app.driver.current = undefined;
    localStorage.setItem(APPS_KEY, JSON.stringify(apps)); localStorage.setItem(AUDIT_KEY, JSON.stringify([audit, ...readAudit()])); return audit;
  }
  async deleteApplication(namespace: string, name: string, operator: string, reason?: string) {
    await wait(350); const apps = readApps(); const index = apps.findIndex((item) => item.namespace === namespace && item.name === name);
    if (index < 0) throw new Error('Application not found');
    const app = apps[index];
    if (!['COMPLETED', 'FAILED', 'SUBMISSION_FAILED', 'KILLED'].includes(app.state)) throw new Error('Only terminal applications can be deleted');
    const audit: OperationAudit = { id: `op-${Date.now()}`, applicationName: name, namespace, operator, operation: 'DELETE', reason, timestamp: new Date().toISOString(), result: 'SUCCESS', message: 'Terminal SparkApplication deletion accepted by Kubernetes API (mock)' };
    apps.splice(index, 1);
    localStorage.setItem(APPS_KEY, JSON.stringify(apps)); localStorage.setItem(AUDIT_KEY, JSON.stringify([audit, ...readAudit()])); return audit;
  }
  async reset() { await wait(120); localStorage.setItem(APPS_KEY, JSON.stringify(cloneSeed())); localStorage.removeItem(AUDIT_KEY); }
}

export class ApiSparkApplicationService implements SparkApplicationService {
  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), runtimeConfig.api.requestTimeoutMs);
    try {
      const response = await fetch(apiUrl(path), {
        ...init,
        credentials: 'include',
        signal: controller.signal,
        headers: { Accept: 'application/json', ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers },
      });
      if (!response.ok) {
        const detail = await response.json().catch(() => ({ message: response.statusText })) as { message?: string };
        throw new Error(detail.message || `API request failed (${response.status})`);
      }
      if (response.status === 204) return undefined as T;
      return await response.json() as T;
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw new Error('API request timed out');
      throw error;
    } finally {
      window.clearTimeout(timeout);
    }
  }

  private query(filters: ApplicationFilters = {}) {
    const params = new URLSearchParams();
    Object.entries(filters).forEach(([key, value]) => { if (value) params.set(key, value); });
    const query = params.toString();
    return query ? `?${query}` : '';
  }

  listApplications(filters: ApplicationFilters = {}) { return this.request<SparkApplication[]>(`/v1/applications${this.query(filters)}`); }
  getSummary(filters: ApplicationFilters = {}) { return this.request<DashboardSummary>(`/v1/dashboard/summary${this.query(filters)}`); }
  getApplication(namespace: string, name: string) { return this.request<SparkApplication>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(name)}`); }
  submitApplication(namespace: string, yaml: string, operator: string) {
    return this.request<SparkApplication>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications`, {
      method: 'POST', body: JSON.stringify({ yaml, requestedBy: operator }),
    });
  }
  getAudit() { return this.request<OperationAudit[]>('/v1/audit'); }
  killApplication(namespace: string, name: string, operator: string, reason?: string) {
    return this.request<OperationAudit>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(name)}/kill`, {
      method: 'POST', body: JSON.stringify({ reason, requestedBy: operator }),
    });
  }
  deleteApplication(namespace: string, name: string, operator: string, reason?: string) {
    return this.request<OperationAudit>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(name)}`, {
      method: 'DELETE', body: JSON.stringify({ reason, requestedBy: operator }),
    });
  }
  reset() { return this.request<void>('/v1/demo/reset', { method: 'POST' }); }
}

export const sparkService: SparkApplicationService = runtimeConfig.dataMode === 'api'
  ? new ApiSparkApplicationService()
  : new MockSparkApplicationService();
