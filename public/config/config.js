window.__SPARK_CONTROL_CENTER_CONFIG__ = {
  appName: 'Spark Control Center',
  timeZone: '',
  dataMode: 'mock',
  api: {
    baseUrl: '/api',
    requestTimeoutMs: 15000,
  },
  auth: {
    mode: 'mock',
    oidc: {
      enabled: false,
      providerName: 'Keycloak',
      issuerUrl: '',
      clientId: 'spark-control-center',
      audience: '',
      scopes: ['openid', 'profile', 'email', 'groups'],
      authorizePath: '/v1/auth/oidc/login',
      loginPath: '/v1/auth/login',
      logoutPath: '/v1/auth/logout',
      sessionPath: '/v1/auth/me',
    },
  },
  cluster: {
    name: 'spark-demo',
    namespaces: ['spark-prod', 'spark-ml', 'spark-streaming', 'spark-sandbox'],
  },
  dashboard: {
    refreshIntervalSeconds: 10,
    defaultHistoryDays: 7,
  },
  historyServer: {
    enabled: false,
    baseUrl: '',
  },
  features: {
    kill: true,
    audit: true,
    sparkUi: true,
    submit: true,
    executorLogs: true,
  },
};
