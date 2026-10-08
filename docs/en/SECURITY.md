# Security policy

## Reporting vulnerabilities

Use the repository's GitHub private vulnerability reporting feature. Do not open public issues containing exploit details, credentials, tokens, cluster addresses or other sensitive information. Include the affected version, deployment mode, reproduction steps, impact and suggested mitigation. Maintainers coordinate remediation and disclosure through the private report.

## Deployment responsibilities

- Store database passwords, local administrator passwords and OIDC client secrets in Kubernetes Secrets.
- Terminate TLS before exposing the console.
- Limit Kubernetes RBAC and watched namespaces.
- Protect network access to Prometheus, Loki, PostgreSQL and the identity provider.
- Rotate credentials accidentally disclosed in configuration or logs.
- Review container image provenance and deployment configuration.
- Back up PostgreSQL, which holds identity, audit, workflow and scheduling state.

The root [security policy](../../SECURITY.md) is the entry point for GitHub vulnerability reports.
