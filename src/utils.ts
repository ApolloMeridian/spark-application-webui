import dayjs from 'dayjs';
import timezone from 'dayjs/plugin/timezone';
import utc from 'dayjs/plugin/utc';
import type { Dayjs } from 'dayjs';
import { runtimeConfig } from './runtimeConfig';
import type { ApplicationState, ResourceAmount, SparkApplication, UserRole } from './types';

dayjs.extend(utc);
dayjs.extend(timezone);

export function displayTime(value?: string | Date | Dayjs): Dayjs {
  const parsed = dayjs(value);
  const configured = runtimeConfig.timeZone.trim();
  if (!configured) return parsed;
  try { return parsed.tz(configured); } catch { return parsed; }
}

export function formatTimestamp(value?: string | Date | Dayjs, pattern = 'YYYY-MM-DD HH:mm:ss'): string {
  if (!value) return '—';
  const parsed = displayTime(value);
  return parsed.isValid() ? parsed.format(pattern) : '—';
}

export function displayNow(): Dayjs {
  return displayTime(new Date());
}

export const stateColor: Record<ApplicationState, string> = {
  RUNNING: '#12a878',
  COMPLETED: '#2878ff',
  FAILED: '#e44c55',
  SUBMISSION_FAILED: '#c9323d',
  PENDING: '#e5a11a',
  SUBMITTED: '#d88a00',
  FAILING: '#ef781f',
  UNKNOWN: '#7d8799',
  KILLED: '#697386',
};

export function formatCpu(value?: number): string {
  if (value === undefined) return '—';
  return `${value.toFixed(value >= 10 ? 1 : 2).replace(/\.0$/, '')} C`;
}

export function formatMemory(value?: number): string {
  if (value === undefined) return '—';
  if (value >= 1024) return `${(value / 1024).toFixed(2)} TiB`;
  return `${value.toFixed(value >= 10 ? 0 : 1)} GiB`;
}

export function formatDuration(app: SparkApplication, now = dayjs()): string {
  const start = dayjs(app.startedAt ?? app.createdAt);
  const end = app.finishedAt ? dayjs(app.finishedAt) : now;
  const seconds = Math.max(0, end.diff(start, 'second'));
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return h ? `${h}h ${m}m` : m ? `${m}m ${s}s` : `${s}s`;
}

export function sumResources(app: SparkApplication): { requested: ResourceAmount; used: ResourceAmount } {
  return app.executors.reduce(
    (acc, executor) => ({
      requested: {
        cpu: acc.requested.cpu + executor.resources.request.cpu,
        memoryGiB: acc.requested.memoryGiB + executor.resources.request.memoryGiB,
      },
      used: {
        cpu: acc.used.cpu + (executor.resources.current?.cpu ?? 0),
        memoryGiB: acc.used.memoryGiB + (executor.resources.current?.memoryGiB ?? 0),
      },
    }),
    {
      requested: { ...app.driver.request },
      used: { cpu: app.driver.current?.cpu ?? 0, memoryGiB: app.driver.current?.memoryGiB ?? 0 },
    },
  );
}

export function canKill(role: UserRole): boolean {
	return role === 'admin';
}

export function isKillableState(state: ApplicationState): boolean {
  return state === 'RUNNING' || state === 'SUBMITTED';
}

export function canDelete(role: UserRole): boolean {
  return role === 'admin';
}

export function canSubmit(role: UserRole): boolean {
	return role === 'admin';
}

export function isTerminalState(state: ApplicationState): boolean {
  return ['COMPLETED', 'FAILED', 'SUBMISSION_FAILED', 'KILLED'].includes(state);
}
