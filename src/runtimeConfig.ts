export interface RuntimeConfig {
  appName: string;
  timeZone: string;
  dataMode: 'mock' | 'api';
  api: {
    baseUrl: string;
    requestTimeoutMs: number;
  };
  auth: {
    mode: 'mock' | 'local' | 'oidc';
    oidc: {
      enabled: boolean;
      providerName: string;
      issuerUrl: string;
      clientId: string;
      audience: string;
      scopes: string[];
      authorizePath: string;
      loginPath: string;
      logoutPath: string;
      sessionPath: string;
    };
  };
  cluster: {
    name: string;
    namespaces: string[];
  };
  dashboard: {
    refreshIntervalSeconds: number;
    defaultHistoryDays: number;
  };
  historyServer: {
    enabled: boolean;
    baseUrl: string;
  };
  features: {
    kill: boolean;
    audit: boolean;
    sparkUi: boolean;
    submit: boolean;
    executorLogs: boolean;
  };
}

export type RuntimeConfigInput = Partial<Omit<RuntimeConfig, 'api' | 'auth' | 'cluster' | 'dashboard' | 'historyServer' | 'features'>> & {
  api?: Partial<RuntimeConfig['api']>;
  auth?: Partial<Omit<RuntimeConfig['auth'], 'oidc'>> & { oidc?: Partial<RuntimeConfig['auth']['oidc']> };
  cluster?: Partial<RuntimeConfig['cluster']>;
  dashboard?: Partial<RuntimeConfig['dashboard']>;
  historyServer?: Partial<RuntimeConfig['historyServer']>;
  features?: Partial<RuntimeConfig['features']>;
};

const defaults: RuntimeConfig = {
  appName: 'Spark Control Center',
  timeZone: '',
  dataMode: 'mock',
  api: { baseUrl: '/api', requestTimeoutMs: 15000 },
  auth: {
    mode: 'mock',
    oidc: {
      enabled: false, providerName: 'Keycloak', issuerUrl: '', clientId: 'spark-control-center', audience: '', scopes: ['openid', 'profile', 'email', 'groups'],
      authorizePath: '/v1/auth/oidc/login',
      loginPath: '/v1/auth/login', logoutPath: '/v1/auth/logout', sessionPath: '/v1/auth/me',
    },
  },
  cluster: { name: 'spark-demo', namespaces: ['spark-prod', 'spark-ml', 'spark-streaming', 'spark-sandbox'] },
  dashboard: { refreshIntervalSeconds: 10, defaultHistoryDays: 7 },
  historyServer: { enabled: false, baseUrl: '' },
  features: { kill: true, audit: true, sparkUi: true, submit: true, executorLogs: true },
};

function normalizeBaseUrl(value: string) {
  const trimmed = value.trim();
  return trimmed === '/' ? '' : trimmed.replace(/\/$/, '');
}

export function resolveRuntimeConfig(input: RuntimeConfigInput = {}): RuntimeConfig {
  return {
    ...defaults,
    ...input,
    api: { ...defaults.api, ...input.api, baseUrl: normalizeBaseUrl(input.api?.baseUrl ?? defaults.api.baseUrl) },
    auth: {
      ...defaults.auth,
      ...input.auth,
      oidc: { ...defaults.auth.oidc, ...input.auth?.oidc },
    },
    cluster: { ...defaults.cluster, ...input.cluster },
    dashboard: { ...defaults.dashboard, ...input.dashboard },
    historyServer: { ...defaults.historyServer, ...input.historyServer, baseUrl: normalizeBaseUrl(input.historyServer?.baseUrl ?? defaults.historyServer.baseUrl) },
    features: { ...defaults.features, ...input.features },
  };
}

export const runtimeConfig = resolveRuntimeConfig(window.__SPARK_CONTROL_CENTER_CONFIG__);

export function apiUrl(path: string) {
  const normalized = path.startsWith('/') ? path : `/${path}`;
  return `${runtimeConfig.api.baseUrl}${normalized}`;
}
