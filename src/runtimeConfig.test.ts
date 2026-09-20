import { describe, expect, it } from 'vitest';
import { resolveRuntimeConfig } from './runtimeConfig';

describe('runtime configuration', () => {
  it('merges environment overrides without dropping nested defaults', () => {
    const config = resolveRuntimeConfig({
      dataMode: 'api',
      api: { baseUrl: 'https://api.example.com/', requestTimeoutMs: 30000 },
      auth: { mode: 'oidc', oidc: { issuerUrl: 'https://id.example.com/realms/data' } },
      historyServer: { enabled: true, baseUrl: 'https://shs.example.com/history/' },
    });
    expect(config.dataMode).toBe('api');
    expect(config.api.baseUrl).toBe('https://api.example.com');
    expect(config.auth.oidc.issuerUrl).toContain('/realms/data');
    expect(config.auth.oidc.loginPath).toBe('/v1/auth/login');
    expect(config.historyServer.baseUrl).toBe('https://shs.example.com/history');
  });

  it('uses safe mock defaults when config.js is absent', () => {
    const config = resolveRuntimeConfig();
    expect(config.dataMode).toBe('mock');
    expect(config.auth.mode).toBe('mock');
    expect(config.dashboard.refreshIntervalSeconds).toBe(10);
    expect(config.dashboard.defaultHistoryDays).toBe(7);
  });
});
