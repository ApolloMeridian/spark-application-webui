import type { AlertRule, ApplicationAlert, ApplicationChange, ApplicationFavorite, ApplicationFilters, ApplicationList, ApplicationTemplate, BatchActionItem, BatchActionResult, DashboardSummary, FailureFingerprint, LogEntry, ManifestPreview, MetricPoint, OperationAudit, SparkApplication, SparkApplicationService } from './types';
import { seedApplications } from './mockData';
import { apiUrl, runtimeConfig } from './runtimeConfig';
import { sumResources } from './utils';

const APPS_KEY = 'spark-console-mock-applications';
const AUDIT_KEY = 'spark-console-mock-audit';
const TEMPLATES_KEY = 'spark-console-mock-templates';
const ALERT_RULES_KEY = 'spark-console-mock-alert-rules';
const wait = (ms = 220) => new Promise((resolve) => setTimeout(resolve, ms));

function cloneSeed() { return JSON.parse(JSON.stringify(seedApplications)) as SparkApplication[]; }
function readApps(): SparkApplication[] {
  const stored = localStorage.getItem(APPS_KEY);
  if (!stored) { const initial = cloneSeed(); localStorage.setItem(APPS_KEY, JSON.stringify(initial)); return initial; }
  return JSON.parse(stored);
}
function readAudit(): OperationAudit[] { return JSON.parse(localStorage.getItem(AUDIT_KEY) || '[]'); }
const demoTemplate: ApplicationTemplate = { id: 'tpl-streaming', name: 'Streaming baseline', description: 'Reusable Spark streaming job with validated image and parallelism parameters.', namespace: 'spark-prod', manifest: 'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: {{name}}\n  namespace: spark-prod\nspec:\n  image: {{image}}\n  executor:\n    instances: {{executors}}\n', parameters: [{ name: 'name', type: 'string', required: true, pattern: '^[a-z0-9-]+$' }, { name: 'image', type: 'string', required: true, default: 'spark:3.5.3' }, { name: 'executors', type: 'integer', required: true, default: '2' }], version: 3, disabled: false, createdBy: 'platform-admin', createdAt: '2026-09-20T08:00:00Z', updatedAt: '2026-09-28T02:00:00Z' };
function readTemplates() { return JSON.parse(localStorage.getItem(TEMPLATES_KEY) || JSON.stringify([demoTemplate])) as ApplicationTemplate[]; }
function matches(app: SparkApplication, filters: ApplicationFilters = {}) {
  const keyword = filters.keyword?.toLowerCase().trim();
  return (!keyword || [app.name, app.owner].some((value) => value.toLowerCase().includes(keyword)))
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
  async getApplicationMetrics(namespace: string, name: string, from: string, to: string) {
    await wait(150);
    const app = await this.getApplication(namespace, name);
    const start = new Date(from).getTime(); const end = new Date(to).getTime();
    return app.metrics.filter((point) => { const value = new Date(point.time).getTime(); return value >= start && value <= end; });
  }
  async getExecutorLogs(namespace: string, application: string, pod: string, from: string, to: string, direction: 'forward' | 'backward') {
    await wait(220);
    const app = await this.getApplication(namespace, application);
    if (!app.executors.some((executor) => executor.name === pod)) throw new Error('Executor not found');
    const start = new Date(from).getTime(); const end = new Date(to).getTime();
    const base = new Date(app.startedAt ?? app.createdAt).getTime();
    const entries: LogEntry[] = app.logs.map((line, index) => ({ timestamp: new Date(base + index * 1000).toISOString(), line: `[${pod}] ${line}`, labels: { namespace, pod, spark_role: 'executor' } })).filter((entry) => { const value = new Date(entry.timestamp).getTime(); return value >= start && value <= end; });
    return direction === 'backward' ? entries.reverse() : entries;
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
      owner: operator || 'unknown', state: 'SUBMITTED', createdAt: now, startedAt: undefined, finishedAt: undefined,
      sparkApplicationId: undefined, sparkUiAvailable: false, eventLogEnabled: false, submissionId: `mock-${Date.now()}`, driverPod: `${name}-driver`,
      driverNode: undefined, driverNodePool: undefined, executors: [], metrics: [], events: [], logs: [],
      yaml, pendingReason: undefined, errorMessage: undefined,
    };
    const audit: OperationAudit = { id: `op-${Date.now()}`, applicationName: name, namespace, operator, operation: 'SUBMIT', timestamp: now, result: 'SUCCESS', message: 'SparkApplication created by Kubernetes API (mock)' };
    apps.unshift(app); localStorage.setItem(APPS_KEY, JSON.stringify(apps)); localStorage.setItem(AUDIT_KEY, JSON.stringify([audit, ...readAudit()]));
    return app;
  }
  async dryRunApplication(namespace: string, yaml: string, _operator: string): Promise<ManifestPreview> {
    await wait(300);
    if (!/^apiVersion:\s*sparkoperator\.k8s\.io\/v1beta2\s*$/m.test(yaml) || !/^kind:\s*SparkApplication\s*$/m.test(yaml)) throw new Error('YAML must be a sparkoperator.k8s.io/v1beta2 SparkApplication');
    const metadata = yaml.match(/^metadata:\s*\n((?:[ \t]+.*\n?)*)/m)?.[1] ?? '';
    const name = metadata.match(/^\s+name:\s*["']?([^\s"']+)["']?\s*$/m)?.[1];
    if (!name) throw new Error('YAML metadata.name is required');
    if (readApps().some((app) => app.namespace === namespace && app.name === name)) throw new Error('SparkApplication already exists');
    const serverYaml = /^\s*namespace:/m.test(metadata) ? yaml : yaml.replace(/^metadata:\s*$/m, `metadata:\n  namespace: ${namespace}`);
    return { name, namespace, originalYaml: yaml, serverYaml, warnings: [], dryRunAccepted: true };
  }
  async prepareApplication(namespace: string, name: string, mode: 'clone' | 'retry'): Promise<ManifestPreview> {
    const app = await this.getApplication(namespace, name);
    const suffix = mode === 'clone' ? '-copy' : `-retry-${new Date().toISOString().slice(2, 16).replace(/[-T:]/g, '')}`;
    const suggestedName = `${name.slice(0, Math.max(1, 63 - suffix.length))}${suffix}`;
    let manifest = app.yaml;
    try {
      const object = JSON.parse(app.yaml) as Record<string, unknown>;
      const metadata = (object.metadata ?? {}) as Record<string, unknown>;
      metadata.name = suggestedName; metadata.namespace = namespace;
      ['uid', 'resourceVersion', 'generation', 'creationTimestamp', 'managedFields', 'deletionTimestamp'].forEach((key) => delete metadata[key]);
      delete object.status; object.metadata = metadata;
      manifest = JSON.stringify(object, null, 2);
    } catch {
      manifest = manifest.replace(/(^\s*name:\s*)\S+/m, `$1${suggestedName}`).replace(/(^\s*namespace:\s*)\S+/m, `$1${namespace}`);
    }
    return { name: suggestedName, namespace, originalYaml: '', serverYaml: manifest, warnings: [], dryRunAccepted: false };
  }
  async killApplication(namespace: string, name: string, operator: string, reason?: string) {
    await wait(550); const apps = readApps(); const app = apps.find((item) => item.namespace === namespace && item.name === name);
    if (!app) throw new Error('Application not found');
    if (!['RUNNING', 'SUBMITTED'].includes(app.state)) throw new Error('Only RUNNING or SUBMITTED applications can be killed');
    const audit: OperationAudit = { id: `op-${Date.now()}`, applicationName: name, namespace, operator, operation: 'KILL', reason, timestamp: new Date().toISOString(), result: 'SUCCESS', message: 'Driver pod terminated and retained; executor pods cleaned; SparkApplication retained (mock)' };
    app.state = 'FAILED'; app.finishedAt = new Date().toISOString(); app.errorMessage = 'Driver was terminated and retained for diagnostics'; app.sparkUiAvailable = false; app.executors = []; app.driver.current = undefined;
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
        if (response.status === 401) window.dispatchEvent(new Event('spark-console:unauthorized'));
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
  getApplicationMetrics(namespace: string, name: string, from: string, to: string) {
    const params = new URLSearchParams({ from, to });
    return this.request<MetricPoint[]>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(name)}/metrics?${params.toString()}`);
  }
  getExecutorLogs(namespace: string, application: string, pod: string, from: string, to: string, direction: 'forward' | 'backward') {
    const params = new URLSearchParams({ from, to, direction, limit: '5000' });
    return this.request<LogEntry[]>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(application)}/executors/${encodeURIComponent(pod)}/logs?${params.toString()}`);
  }
  submitApplication(namespace: string, yaml: string, operator: string) {
    return this.request<SparkApplication>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications`, {
      method: 'POST', body: JSON.stringify({ yaml, requestedBy: operator }),
    });
  }
  dryRunApplication(namespace: string, yaml: string, operator: string) {
    return this.request<ManifestPreview>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/dry-run`, {
      method: 'POST', body: JSON.stringify({ yaml, requestedBy: operator }),
    });
  }
  prepareApplication(namespace: string, name: string, mode: 'clone' | 'retry') {
    return this.request<ManifestPreview>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(name)}/prepare?mode=${mode}`);
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

export function subscribeApplicationEvents(listener: (change: ApplicationChange) => void) {
  if (runtimeConfig.dataMode !== 'api' || typeof EventSource === 'undefined') return () => undefined;
  const source = new EventSource(apiUrl('/v1/stream'), { withCredentials: true });
  const handler = (event: MessageEvent<string>) => {
    try { listener(JSON.parse(event.data) as ApplicationChange); } catch { /* ignore malformed events */ }
  };
  source.addEventListener('application', handler as EventListener);
  return () => source.close();
}

async function managementRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiUrl(path), { ...init, credentials: 'include', headers: { Accept: 'application/json', ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers } });
  if (!response.ok) { const detail = await response.json().catch(() => ({ message: response.statusText })) as { message?: string }; throw new Error(detail.message || `API request failed (${response.status})`); }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

function filterQuery(filters: ApplicationFilters) { const params = new URLSearchParams(); Object.entries(filters).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)); }); return params.toString(); }

class OperationsService {
  async listApplications(filters: ApplicationFilters): Promise<ApplicationList> {
    if (runtimeConfig.dataMode === 'mock') { const items = await sparkService.listApplications(filters); const page = filters.page ?? 1; const pageSize = filters.pageSize ?? 20; return { items: items.slice((page - 1) * pageSize, page * pageSize), page, pageSize, total: items.length }; }
    return managementRequest<ApplicationList>(`/v1/applications?${filterQuery(filters)}`);
  }
  listTemplates(includeDisabled = false) { return runtimeConfig.dataMode === 'mock' ? Promise.resolve(readTemplates().filter((x) => includeDisabled || !x.disabled)) : managementRequest<ApplicationTemplate[]>(`/v1/templates?includeDisabled=${includeDisabled}`); }
  getTemplate(id: string) { if (runtimeConfig.dataMode === 'mock') { const item = readTemplates().find((x) => x.id === id); return item ? Promise.resolve(item) : Promise.reject(new Error('Template not found')); } return managementRequest<ApplicationTemplate>(`/v1/templates/${encodeURIComponent(id)}`); }
  saveTemplate(template: Partial<ApplicationTemplate>) { if (runtimeConfig.dataMode === 'mock') { const items = readTemplates(); const now = new Date().toISOString(); const index = items.findIndex((x) => x.id === template.id); const saved = { ...demoTemplate, ...template, id: template.id || `tpl-${Date.now()}`, version: index >= 0 ? items[index].version + 1 : 1, createdAt: index >= 0 ? items[index].createdAt : now, updatedAt: now } as ApplicationTemplate; if (index >= 0) items[index] = saved; else items.unshift(saved); localStorage.setItem(TEMPLATES_KEY, JSON.stringify(items)); return Promise.resolve(saved); } const editing = Boolean(template.id); return managementRequest<ApplicationTemplate>(editing ? `/v1/templates/${encodeURIComponent(template.id!)}` : '/v1/templates', { method: editing ? 'PATCH' : 'POST', body: JSON.stringify(template) }); }
  deleteTemplate(id: string) { if (runtimeConfig.dataMode === 'mock') { localStorage.setItem(TEMPLATES_KEY, JSON.stringify(readTemplates().filter((x) => x.id !== id))); return Promise.resolve(); } return managementRequest<void>(`/v1/templates/${encodeURIComponent(id)}`, { method: 'DELETE' }); }
  async copyTemplate(id: string, name: string) { if (runtimeConfig.dataMode === 'mock') { const item = await this.getTemplate(id); return this.saveTemplate({ ...item, id: undefined, name }); } return managementRequest<ApplicationTemplate>(`/v1/templates/${encodeURIComponent(id)}/copy`, { method: 'POST', body: JSON.stringify({ name }) }); }
  async renderTemplate(id: string, values: Record<string, string>) { if (runtimeConfig.dataMode === 'mock') { const item = await this.getTemplate(id); let manifest = item.manifest; item.parameters.forEach((p) => { manifest = manifest.replaceAll(`{{${p.name}}}`, values[p.name] || p.default || ''); }); return { name: values.name || item.name, namespace: item.namespace, originalYaml: item.manifest, serverYaml: manifest, warnings: [], dryRunAccepted: false }; } return managementRequest<ManifestPreview>(`/v1/templates/${encodeURIComponent(id)}/render`, { method: 'POST', body: JSON.stringify({ values }) }); }
  listFavorites() { return runtimeConfig.dataMode === 'mock' ? Promise.resolve(JSON.parse(localStorage.getItem('spark-console-favorites') || '[]') as ApplicationFavorite[]) : managementRequest<ApplicationFavorite[]>('/v1/favorites'); }
  setFavorite(namespace: string, application: string, favorite: boolean) { if (runtimeConfig.dataMode === 'mock') { const items = JSON.parse(localStorage.getItem('spark-console-favorites') || '[]') as ApplicationFavorite[]; const next = favorite ? [...items.filter((x) => x.namespace !== namespace || x.application !== application), { namespace, application }] : items.filter((x) => x.namespace !== namespace || x.application !== application); localStorage.setItem('spark-console-favorites', JSON.stringify(next)); return Promise.resolve(); } return managementRequest<void>(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(application)}/favorite`, { method: 'PUT', body: JSON.stringify({ favorite }) }); }
  async batchPreview(operation: 'kill' | 'delete', items: BatchActionItem[]) { if (runtimeConfig.dataMode === 'mock') { return Promise.all(items.map(async (item) => { const app = await sparkService.getApplication(item.namespace, item.name); const allowed = operation === 'kill' ? ['RUNNING', 'SUBMITTED'].includes(app.state) : ['COMPLETED', 'FAILED', 'SUBMISSION_FAILED', 'KILLED'].includes(app.state); return { ...item, allowed, reason: allowed ? undefined : `Application state ${app.state} is not eligible` }; })); } return managementRequest<BatchActionItem[]>('/v1/applications/batch/preview', { method: 'POST', body: JSON.stringify({ operation, items }) }); }
  async batchExecute(operation: 'kill' | 'delete', items: BatchActionItem[], reason: string) { if (runtimeConfig.dataMode === 'mock') { const audits: OperationAudit[] = []; for (const item of items) { try { audits.push(operation === 'kill' ? await sparkService.killApplication(item.namespace, item.name, 'demo-user', reason) : await sparkService.deleteApplication(item.namespace, item.name, 'demo-user', reason)); } catch (error) { audits.push({ id: `op-${Date.now()}-${audits.length}`, applicationName: item.name, namespace: item.namespace, operator: 'demo-user', operation: operation.toUpperCase() as 'KILL' | 'DELETE', reason, timestamp: new Date().toISOString(), result: 'FAILED', message: error instanceof Error ? error.message : String(error) }); } } return { operation: operation.toUpperCase(), items: audits } as BatchActionResult; } return managementRequest<BatchActionResult>('/v1/applications/batch/execute', { method: 'POST', body: JSON.stringify({ operation, items, reason }) }); }
  listAlerts() { return runtimeConfig.dataMode === 'mock' ? Promise.resolve([{ id: 'alert-image-pull', ruleId: 'rule-retries', ruleName: 'Repeated retries', namespace: 'spark-prod', applicationName: 'image-pull-demo', fingerprint: 'image-pull-backoff', severity: 'error', status: 'active', summary: 'Repeated Kubernetes failures or retries detected', evidence: ['failure event count=14', 'ImagePullBackOff'], confidence: 'high', recommendation: 'Verify the image name, registry reachability, and imagePullSecrets.', firstSeenAt: '2026-09-28T02:00:00Z', lastSeenAt: '2026-09-28T02:12:00Z' }] as ApplicationAlert[]) : managementRequest<ApplicationAlert[]>('/v1/alerts'); }
  updateAlert(id: string, status: 'active' | 'acknowledged' | 'silenced', silencedUntil?: string) { if (runtimeConfig.dataMode === 'mock') return Promise.resolve(); return managementRequest<void>(`/v1/alerts/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ status, silencedUntil }) }); }
  listAlertRules() { if (runtimeConfig.dataMode === 'mock') { const seed = [{ id: 'rule-retries', name: 'Repeated retries', type: 'retries', namespaces: ['spark-prod'], enabled: true, minimumRetries: 3, severity: 'error', notifyWebhook: true, createdBy: 'platform-admin', createdAt: '2026-09-20T08:00:00Z', updatedAt: '2026-09-28T02:00:00Z' }] as AlertRule[]; return Promise.resolve(JSON.parse(localStorage.getItem(ALERT_RULES_KEY) || JSON.stringify(seed)) as AlertRule[]); } return managementRequest<AlertRule[]>('/v1/alert-rules'); }
  async saveAlertRule(rule: Partial<AlertRule>) { if (runtimeConfig.dataMode === 'mock') { const items = await this.listAlertRules(); const now = new Date().toISOString(); const index = items.findIndex((x) => x.id === rule.id); const saved = { ...rule, id: rule.id || `rule-${Date.now()}`, createdBy: 'demo-user', createdAt: index >= 0 ? items[index].createdAt : now, updatedAt: now } as AlertRule; if (index >= 0) items[index] = saved; else items.unshift(saved); localStorage.setItem(ALERT_RULES_KEY, JSON.stringify(items)); return saved; } const editing = Boolean(rule.id); return managementRequest<AlertRule>(editing ? `/v1/alert-rules/${encodeURIComponent(rule.id!)}` : '/v1/alert-rules', { method: editing ? 'PATCH' : 'POST', body: JSON.stringify(rule) }); }
  async deleteAlertRule(id: string) { if (runtimeConfig.dataMode === 'mock') { localStorage.setItem(ALERT_RULES_KEY, JSON.stringify((await this.listAlertRules()).filter((x) => x.id !== id))); return; } return managementRequest<void>(`/v1/alert-rules/${encodeURIComponent(id)}`, { method: 'DELETE' }); }
  listFingerprints() { return runtimeConfig.dataMode === 'mock' ? Promise.resolve([{ fingerprint: 'IMAGE_PULL', code: 'IMAGE_PULL', count: 6, lastSeenAt: '2026-09-28T02:12:00Z', sampleApplication: 'image-pull-demo', namespace: 'spark-prod', severity: 'error', recommendation: 'Verify image names and registry credentials.' }] as FailureFingerprint[]) : managementRequest<FailureFingerprint[]>('/v1/diagnostics/fingerprints'); }
}

export const operationsService = new OperationsService();
