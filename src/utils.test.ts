import { describe, expect, it } from 'vitest';
import { canDelete, canKill, canSubmit, formatCpu, formatMemory, isKillableState, isTerminalState, stateColor, sumResources } from './utils';
import { seedApplications } from './mockData';

describe('formatters and permissions', () => {
  it('formats Kubernetes resource amounts', () => {
    expect(formatCpu(2)).toBe('2.00 C');
    expect(formatCpu(undefined)).toBe('—');
    expect(formatMemory(1536)).toBe('1.50 TiB');
    expect(formatMemory(undefined)).toBe('—');
  });
  it('maps every application state to a color', () => {
    expect(Object.keys(stateColor)).toHaveLength(9);
    expect(stateColor.RUNNING).toBe('#12a878');
    expect(stateColor.SUBMISSION_FAILED).toBeTruthy();
  });
  it('enforces kill permissions', () => {
    expect(canKill('viewer')).toBe(false);
    expect(canKill('admin')).toBe(true);
    expect(canDelete('viewer')).toBe(false);
    expect(canDelete('admin')).toBe(true);
    expect(canSubmit('viewer')).toBe(false);
    expect(canSubmit('admin')).toBe(true);
    expect(isTerminalState('COMPLETED')).toBe(true);
    expect(isTerminalState('RUNNING')).toBe(false);
    expect(isKillableState('RUNNING')).toBe(true);
    expect(isKillableState('SUBMITTED')).toBe(true);
    expect(isKillableState('COMPLETED')).toBe(false);
  });
  it('aggregates driver and executor resources', () => {
    const app = seedApplications[0];
    const totals = sumResources(app);
    expect(totals.requested.cpu).toBe(app.driver.request.cpu + app.executors.length * 4);
    expect(totals.used.cpu).toBeGreaterThan(0);
  });
});
