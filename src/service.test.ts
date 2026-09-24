import { beforeEach, describe, expect, it } from 'vitest';
import { sparkService } from './service';

describe('mock application service', () => {
  beforeEach(async () => { localStorage.clear(); await sparkService.reset(); });
  it('lists and filters applications', async () => {
    const running = await sparkService.listApplications({ state: 'RUNNING' });
    expect(running.length).toBeGreaterThan(0);
    expect(running.every((app) => app.state === 'RUNNING')).toBe(true);
  });
  it('kills an application and records audit', async () => {
    const before = await sparkService.getApplication('spark-prod', 'parquet-to-iceberg');
    expect(before.state).toBe('RUNNING');
    await sparkService.killApplication(before.namespace, before.name, 'tester', 'test kill');
    const after = await sparkService.getApplication(before.namespace, before.name);
    const audit = await sparkService.getAudit();
    expect(after.state).toBe('FAILED');
    expect(after.executors).toHaveLength(0);
    expect(audit[0]).toMatchObject({ applicationName: before.name, operator: 'tester', result: 'SUCCESS', reason: 'test kill' });
  });
  it('submits a SparkApplication YAML and records audit', async () => {
    const yaml = 'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: submitted-demo\n  namespace: spark-prod\nspec:\n  image: spark:3.5\n';
    const app = await sparkService.submitApplication('spark-prod', yaml, 'operator');
    expect(app).toMatchObject({ name: 'submitted-demo', namespace: 'spark-prod', state: 'SUBMITTED', owner: 'operator' });
    expect(await sparkService.getApplication('spark-prod', 'submitted-demo')).toBeTruthy();
    expect((await sparkService.getAudit())[0]).toMatchObject({ operation: 'SUBMIT', applicationName: 'submitted-demo' });
  });
  it('validates a manifest with a server-style dry run before submission', async () => {
    const yaml = 'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: dry-run-demo\nspec:\n  image: spark:3.5\n';
    const preview = await sparkService.dryRunApplication('spark-prod', yaml, 'operator');
    expect(preview).toMatchObject({ name: 'dry-run-demo', namespace: 'spark-prod', dryRunAccepted: true });
    expect(preview.serverYaml).toContain('namespace: spark-prod');
  });
  it('prepares a clean clone manifest with a new application name', async () => {
    const preview = await sparkService.prepareApplication('spark-prod', 'parquet-to-iceberg', 'clone');
    expect(preview).toMatchObject({ name: 'parquet-to-iceberg-copy', namespace: 'spark-prod', dryRunAccepted: false });
    expect(preview.serverYaml).toContain('parquet-to-iceberg-copy');
    expect(preview.serverYaml).not.toContain('resourceVersion');
  });
  it('returns executor logs in the requested order', async () => {
    const app = await sparkService.getApplication('spark-prod', 'parquet-to-iceberg');
    const executor = app.executors[0];
    const from = new Date(new Date(app.startedAt!).getTime() - 60000).toISOString();
    const to = new Date(new Date(app.startedAt!).getTime() + 60000).toISOString();
    const forward = await sparkService.getExecutorLogs(app.namespace, app.name, executor.name, from, to, 'forward');
    const backward = await sparkService.getExecutorLogs(app.namespace, app.name, executor.name, from, to, 'backward');
    expect(forward.length).toBeGreaterThan(0);
    expect(backward[0].timestamp).toBe(forward[forward.length - 1].timestamp);
  });
  it('rejects killing an inactive application', async () => {
    await expect(sparkService.killApplication('spark-prod', 'can-merge-hourly', 'tester')).rejects.toThrow('Only RUNNING or SUBMITTED');
  });
  it('kills a submitted application that is stuck before the driver starts', async () => {
    const yaml = 'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: submitted-stuck\n  namespace: spark-prod\nspec:\n  image: spark:3.5\n';
    await sparkService.submitApplication('spark-prod', yaml, 'operator');
    await sparkService.killApplication('spark-prod', 'submitted-stuck', 'operator', 'ImagePullBackOff');
    expect((await sparkService.getApplication('spark-prod', 'submitted-stuck')).state).toBe('FAILED');
  });
  it('deletes only terminal applications and records a DELETE audit', async () => {
    await sparkService.deleteApplication('spark-prod', 'can-merge-hourly', 'admin', 'cleanup');
    await expect(sparkService.getApplication('spark-prod', 'can-merge-hourly')).rejects.toThrow('not found');
    expect((await sparkService.getAudit())[0]).toMatchObject({ operation: 'DELETE', result: 'SUCCESS', reason: 'cleanup' });
    await expect(sparkService.deleteApplication('spark-prod', 'parquet-to-iceberg', 'admin')).rejects.toThrow('terminal');
  });
});
