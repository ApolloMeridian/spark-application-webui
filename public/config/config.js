window.__SPARK_CONTROL_CENTER_CONFIG__ = {
  appName: 'Spark Control Center',
  dataMode: 'mock',
  api: {
    baseUrl: '/api',
    requestTimeoutMs: 15000,
  },
  auth: {
    mode: 'mock',
    oidc: {
      issuerUrl: '',
      clientId: 'spark-control-center',
      audience: '',
      scopes: ['openid', 'profile', 'email', 'roles'],
      loginPath: '/v1/auth/login',
      logoutPath: '/v1/auth/logout',
      sessionPath: '/v1/auth/me',
    },
  },
  cluster: {
    name: 'gke-prod-cn',
    namespaces: ['spark-prod', 'spark-ml', 'spark-streaming', 'spark-sandbox'],
  },
  features: {
    kill: true,
    audit: true,
    demoReset: true,
    sparkUi: true,
  },
};
