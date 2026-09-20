import dayjs from 'dayjs';
import type { ApplicationState, SparkApplication } from './types';

const base = dayjs('2026-09-15T10:00:00+08:00');

function metrics(seed: number, points = 25) {
  return Array.from({ length: points }, (_, index) => ({
    time: base.subtract((points - index) * 2, 'minute').toISOString(),
    cpu: Number((seed * 0.42 + Math.sin(index / 2.8) * seed * 0.1 + index * 0.018).toFixed(2)),
    memoryGiB: Number((seed * 4.8 + Math.cos(index / 4) * seed * 0.35 + index * 0.08).toFixed(1)),
  }));
}

function makeApp(index: number, name: string, state: ApplicationState, namespace: string, owner: string): SparkApplication {
  const running = ['RUNNING', 'FAILING'].includes(state);
  const pending = ['PENDING', 'SUBMITTED'].includes(state);
  const startedAt = pending ? undefined : base.subtract(index * 17 + 24, 'minute').toISOString();
  const executorCount = index % 3 + 3;
  const execs = Array.from({ length: executorCount }, (_, i) => ({
    name: `${name}-exec-${i + 1}`,
    state: (pending && i === executorCount - 1 ? 'PENDING' : state === 'FAILED' && i === 1 ? 'FAILED' : running ? 'RUNNING' : 'SUCCEEDED') as 'RUNNING' | 'PENDING' | 'FAILED' | 'SUCCEEDED',
    resources: {
      request: { cpu: 4, memoryGiB: 30 },
      current: running ? { cpu: Number((2.25 + i * 0.31).toFixed(2)), memoryGiB: 20 + i * 1.7 } : undefined,
      peak: running ? { cpu: Number((3.45 + i * 0.08).toFixed(2)), memoryGiB: 26 + i * 0.6 } : undefined,
    },
    node: pending && i === executorCount - 1 ? undefined : `gke-data-${i % 2 ? 'base' : 'auto'}-${String(i + 1).padStart(2, '0')}`,
    nodePool: (i % 3 === 0 ? 'autoscale' : 'baseline') as 'autoscale' | 'baseline',
    startedAt,
    restarts: state === 'FAILING' && i === 0 ? 2 : 0,
  }));
  const finish = ['COMPLETED', 'FAILED', 'SUBMISSION_FAILED'].includes(state) ? base.subtract(index * 5, 'minute').toISOString() : undefined;
  const pendingReason = state === 'PENDING' ? "0/9 nodes are available: 3 Insufficient memory, 6 node(s) didn't match Pod's node affinity/selector." : undefined;
  const errorMessage = state.includes('FAILED') ? 'ExecutorLostFailure: Container exited with exit code 137 (OOMKilled)' : undefined;
  const driverPod = `${name}-driver`;

  return {
    id: `app-${index + 1}`, name, namespace, cluster: 'gke-prod-cn', owner, team: owner === 'system' ? 'Platform' : 'Data Engineering',
    state, createdAt: base.subtract(index * 17 + 28, 'minute').toISOString(), startedAt, finishedAt: finish,
    image: 'registry.internal/spark-runtime:3.5.3-java17', sparkVersion: '3.5.3',
    sparkApplicationId: pending ? undefined : `spark-20260915-${String(index + 1).padStart(4, '0')}`,
    sparkUiAvailable: state === 'RUNNING', eventLogEnabled: !running && !pending,
    submissionId: `sub-${(70184 + index * 97).toString(16)}`, driverPod,
    driverNode: pending ? undefined : 'gke-data-base-01', driverNodePool: pending ? undefined : 'baseline',
    driver: {
      request: { cpu: 2, memoryGiB: 10 },
      current: running ? { cpu: 1.37 + index * 0.03, memoryGiB: 7.8 } : undefined,
      peak: running ? { cpu: 1.91, memoryGiB: 9.1 } : undefined,
    },
    executors: execs, metrics: running ? metrics(executorCount * 4 + 2) : [], pendingReason, errorMessage,
    events: [
      { id: `${index}-1`, type: 'Normal', reason: 'Scheduled', message: `Successfully assigned ${namespace}/${driverPod} to gke-data-base-01`, source: 'default-scheduler', timestamp: base.subtract(22, 'minute').toISOString(), count: 1 },
      ...(pendingReason ? [{ id: `${index}-2`, type: 'Warning' as const, reason: 'FailedScheduling', message: pendingReason, source: 'default-scheduler', timestamp: base.subtract(3, 'minute').toISOString(), count: 8 }] : []),
      ...(errorMessage ? [{ id: `${index}-3`, type: 'Warning' as const, reason: 'ExecutorFailed', message: errorMessage, source: 'spark-operator', timestamp: base.subtract(2, 'minute').toISOString(), count: 1 }] : []),
    ],
    logs: [
      '2026-09-15 09:31:04 INFO SparkContext: Running Spark version 3.5.3',
      '2026-09-15 09:31:06 INFO KubernetesClusterSchedulerBackend: SchedulerBackend is ready for scheduling',
      '2026-09-15 09:31:08 INFO FileSourceStrategy: Reading data from gs://analytics-prod/input/',
      ...(errorMessage ? [`2026-09-15 09:46:14 ERROR YarnAllocator: ${errorMessage}`, '2026-09-15 09:46:14 WARN TaskSetManager: Lost task 18.0 in stage 9.0'] : []),
      '2026-09-15 09:48:02 INFO MemoryStore: Block broadcast_28 stored as values in memory',
    ],
    yaml: `apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: ${name}\n  namespace: ${namespace}\nspec:\n  type: Scala\n  mode: cluster\n  image: registry.internal/spark-runtime:3.5.3-java17\n  sparkVersion: 3.5.3\n  sparkConf:\n    spark.eventLog.enabled: "${!running && !pending}"\n    spark.eventLog.dir: s3a://spark-history/event-logs\n  driver:\n    cores: 2\n    memory: 8g\n  executor:\n    instances: ${executorCount}\n    cores: 4\n    memory: 24g\nstatus:\n  applicationState:\n    state: ${state}\n`,
  };
}

export const seedApplications: SparkApplication[] = [
  makeApp(0, 'parquet-to-iceberg', 'RUNNING', 'spark-prod', 'jeremy'),
  makeApp(1, 'can-merge-hourly', 'COMPLETED', 'spark-prod', 'system'),
  makeApp(2, 'qb-parser', 'FAILED', 'spark-prod', 'jeremy'),
  makeApp(3, 'customer-feature-daily', 'PENDING', 'spark-ml', 'lina'),
  makeApp(4, 'cdc-compaction', 'SUBMITTED', 'spark-streaming', 'system'),
  makeApp(5, 'fraud-score-backfill', 'FAILING', 'spark-ml', 'morgan'),
  makeApp(6, 'warehouse-reconcile', 'SUBMISSION_FAILED', 'spark-prod', 'lina'),
  makeApp(7, 'ad-hoc-session-42', 'UNKNOWN', 'spark-sandbox', 'alex'),
  makeApp(8, 'recommendation-training', 'RUNNING', 'spark-ml', 'morgan'),
  makeApp(9, 'events-enrichment', 'COMPLETED', 'spark-streaming', 'system'),
];
