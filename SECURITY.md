# Security policy

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting feature for this repository. Do not open a public issue containing exploit details, credentials, tokens, cluster addresses, or other sensitive information.

Include the affected version, deployment mode, reproduction steps, impact, and any suggested mitigation. Maintainers will acknowledge the report and coordinate remediation and disclosure through the private report.

## Deployment responsibility

This project does not ship production credentials. Operators are responsible for:

- storing database passwords, local administrator passwords, and OIDC client secrets in Kubernetes Secrets;
- terminating TLS before exposing the console;
- limiting Kubernetes RBAC and watched namespaces;
- protecting Prometheus, Loki, PostgreSQL, and identity-provider network access;
- rotating any credential accidentally disclosed in configuration or logs;
- reviewing container images and release checksums before deployment.
