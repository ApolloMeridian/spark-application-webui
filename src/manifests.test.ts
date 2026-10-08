import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { cleanApplicationManifest, renderTemplateManifest } from './manifests';

describe('reusable SparkApplication manifests', () => {
  const runtime = {
    apiVersion: 'sparkoperator.k8s.io/v1beta2', kind: 'SparkApplication',
    metadata: { name: 'demo', namespace: 'spark', uid: 'old', ownerReferences: [{ uid: 'old-owner' }], finalizers: ['cleanup'], managedFields: [{ 'f:executorState': { 'f:exec-1': {} } }], labels: { workload: 'etl' }, annotations: { 'kubectl.kubernetes.io/last-applied-configuration': '{"status":{}}', 'spark-control-center.io/submitted-by': 'previous-user', 'custom/config': 'keep' } },
    spec: { image: 'spark:3.5', executor: { instances: 2 } }, status: { driverInfo: { podName: 'old-driver' }, executorState: { 'exec-1': 'RUNNING' }, submissionID: 'old-id' },
  };
  it('cleans JSON snapshots to YAML while preserving the workload configuration', () => {
    const yaml = cleanApplicationManifest(JSON.stringify(runtime), 'spark', 'demo-copy');
    expect(yaml).toContain('kind: SparkApplication');
    const result = parse(yaml);
    expect(result).toEqual({ apiVersion: runtime.apiVersion, kind: runtime.kind, metadata: { name: 'demo-copy', namespace: 'spark', labels: runtime.metadata.labels, annotations: { 'custom/config': 'keep' } }, spec: runtime.spec });
    expect(yaml).not.toMatch(/last-applied|managedFields|executorState|submissionID|previous-user/);
  });
  it('renders typed placeholders without allowing values to inject YAML fields', () => {
    const template = 'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: {{name}}\nspec:\n  executor:\n    instances: {{count}}\n  dynamicAllocation:\n    enabled: "{{enabled}}"\n  arguments: ["{{argument}}"]\n';
    const result = parse(renderTemplateManifest(template, 'spark', [
      { name: 'name', type: 'string', required: true }, { name: 'count', type: 'integer', required: true },
      { name: 'enabled', type: 'boolean', required: false }, { name: 'argument', type: 'string', required: false },
    ], { name: 'new-job', count: '3', enabled: 'true', argument: 'a\nstatus: injected' }));
    expect(result.spec.executor.instances).toBe(3);
    expect(result.spec.dynamicAllocation.enabled).toBe(true);
    expect(result.spec.arguments).toEqual(['a\nstatus: injected']);
    expect(result.status).toBeUndefined();
  });
});
