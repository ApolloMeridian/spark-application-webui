export interface RuntimeConfig {
  appName: string;
  dataMode: 'mock' | 'api';
  api: {
    baseUrl: string;
    requestTimeoutMs: number;
  };
  auth: {
    mode: 'mock' | 'oidc';
    oidc: {
      issuerUrl: string;
      clientId: string;
      audience: string;
      scopes: string[];
      loginPath: string;
      logoutPath: string;
      sessionPath: string;
    };
  };
  cluster: {
    name: string;
    namespaces: string[];
  };
  features: {
    kill: boolean;
    audit: boolean;
    demoReset: boolean;
    sparkUi: boolean;
  };
}

export type RuntimeConfigInput = Partial<Omit<RuntimeConfig, 'api' | 'auth' | 'cluster' | 'features'>> & {
  api?: Partial<RuntimeConfig['api']>;
  auth?: Partial<Omit<RuntimeConfig['auth'], 'oidc'>> & { oidc?: Partial<RuntimeConfig['auth']['oidc']> };
  cluster?: Partial<RuntimeConfig['cluster']>;
  features?: Partial<RuntimeConfig['features']>;
};

const defaults: RuntimeConfig = {
  appName: 'Spark Control Center',
  dataMode: 'mock',
  api: { baseUrl: '/api', requestTimeoutMs: 15000 },
  auth: {
    mode: 'mock',
    oidc: {
      issuerUrl: '', clientId: 'spark-control-center', audience: '', scopes: ['openid', 'profile', 'email', 'roles'],
      loginPath: '/v1/auth/login', logoutPath: '/v1/auth/logout', sessionPath: '/v1/auth/me',
    },
  },
  cluster: { name: 'gke-prod-cn', namespaces: ['spark-prod', 'spark-ml', 'spark-streaming', 'spark-sandbox'] },
  features: { kill: true, audit: true, demoReset: true, sparkUi: true },
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
    features: { ...defaults.features, ...input.features },
  };
}

export const runtimeConfig = resolveRuntimeConfig(window.__SPARK_CONTROL_CENTER_CONFIG__);

export function apiUrl(path: string) {
  const normalized = path.startsWith('/') ? path : `/${path}`;
  return `${runtimeConfig.api.baseUrl}${normalized}`;
}
