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
    expect(after.state).toBe('KILLED');
    expect(audit[0]).toMatchObject({ applicationName: before.name, operator: 'tester', result: 'SUCCESS', reason: 'test kill' });
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
