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
    expect(app).toMatchObject({ name: 'submitted-demo', namespace: 'spark-prod', state: 'SUBMITTED' });
    expect(await sparkService.getApplication('spark-prod', 'submitted-demo')).toBeTruthy();
    expect((await sparkService.getAudit())[0]).toMatchObject({ operation: 'SUBMIT', applicationName: 'submitted-demo' });
  });
  it('rejects killing an inactive application', async () => {
    await expect(sparkService.killApplication('spark-prod', 'can-merge-hourly', 'tester')).rejects.toThrow('Only RUNNING');
  });
  it('deletes only terminal applications and records a DELETE audit', async () => {
    await sparkService.deleteApplication('spark-prod', 'can-merge-hourly', 'admin', 'cleanup');
    await expect(sparkService.getApplication('spark-prod', 'can-merge-hourly')).rejects.toThrow('not found');
    expect((await sparkService.getAudit())[0]).toMatchObject({ operation: 'DELETE', result: 'SUCCESS', reason: 'cleanup' });
    await expect(sparkService.deleteApplication('spark-prod', 'parquet-to-iceberg', 'admin')).rejects.toThrow('terminal');
  });
});
