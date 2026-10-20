# Object storage plugin (1.5.2)

An optional, independently deployed stateless service, not an embedded MinIO Console. It is disabled by default, creates no plugin resources when disabled and requires no storage credentials.

## Build and deploy

```bash
docker build -t spark-control-center:1.5.2 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.5.2 .
docker build -f object-storage/Dockerfile -t spark-control-center-object-storage:1.5.2 .
helm package charts/spark-control-center

# First deployment only: export the production template; preserve existing environment values.
helm show values ./spark-control-center-1.5.2.tgz > values-production.local.yaml
# Adapt images, endpoints, bucket, authentication, database and bootstrap grants in that file.
# Tag and push all three images to your own registry if needed.
helm upgrade --install spark-console ./spark-control-center-1.5.2.tgz \
  --namespace spark-console --create-namespace \
  -f values-production.local.yaml
```

The chart's packaged values are the production template: frontend, backend, local login, optional OIDC, PostgreSQL references, workflows, Ingress and optional storage. Set objectStorage.enabled=true and adapt its connection/grants to enable storage. Installation needs only one `-f`; Helm automatically loads the chart defaults. The copied `values-production.local.yaml` is ignored by Git/Docker. Check all three image repositories/tags, endpoint, buckets, bootstrap username/tenant/prefix, authentication and PostgreSQL before deploying. Back up PostgreSQL before upgrading.

Secrets and CA ConfigMaps remain Kubernetes resources, not additional values files. Reuse existing PostgreSQL/OIDC Secrets and the issuer CA, and prepare the following storage Secrets in the release namespace. Never commit production values, credentials or private keys.

`metadata.annotations` values must be strings. Quote annotation values in values files, especially `"off"`, `"on"`, `"true"`, `"false"` and `"600"`; Helm interprets bare `off` as a boolean. To disable buffering use `"off"`, not `false` or `"false"`. The chart rejects non-string annotations before installation.

## S3 / MinIO

```bash
kubectl -n spark-console create secret generic object-storage-signing \
  --from-literal=signing-key="$(openssl rand -hex 32)"
kubectl -n spark-console create secret generic object-storage-s3-credentials \
  --from-literal=access-key="$S3_ACCESS_KEY" \
  --from-literal=secret-key="$S3_SECRET_KEY"
```

Use a secured administration terminal and avoid retaining credentials in shared shell history. Configure endpoint, region, path style and an explicit bucket allowlist. HTTPS can mount a PEM CA through caConfigMap/caKey; TLS verification cannot be disabled. Set conditionalWrites=true only after verifying conditional PutObject and CompleteMultipartUpload support. Otherwise upload, edit and copy fail closed rather than overwriting unconditionally.

bootstrapGrants explicitly initializes an administrator's data scope. Use `bucket` for one configured bucket, or `allBuckets: true` to grant the selected prefix/actions in every bucket explicitly listed on the connection. This works equally for S3/MinIO and GCS; adding an allowed bucket does not widen ordinary user grants. username must match the actual login name; tenantId is the stored tenant ID. Newly created OIDC users are checked on the next minute's maintenance scan. A bootstrap record applies once: revoking it in the UI survives Pod replacement. Regrant through the UI instead of restarting.

## GCS WIF

Copy the relevant connection and WIF fields from the GKE or generic WIF example into your single production values file. Connections in one Pod share its dedicated KSA. Grant that identity bucket IAM externally; GCS does not use S3 access keys.

- **GKE:** enable cluster/node-pool WIF, authorize the KSA principal on the bucket, or configure IAM service account impersonation through KSA annotations. The client uses metadata/ADC and rejects service-account key files. An annotation alone does not establish WIF.
- **Other Kubernetes:** configure the Google pool/provider, cluster OIDC/JWKS, and IAM externally. Supply an external_account ConfigMap. Helm projects a KSA JWT for each connection. For connection `gcs`, credential_source.file must be `/var/run/secrets/storage-wif/gcs/token`.

```json
{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/example-pool/providers/kubernetes",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://sts.googleapis.com/v1/token",
  "credential_source": {
    "file": "/var/run/secrets/storage-wif/gcs/token",
    "format": { "type": "text" }
  }
}
```

```bash
kubectl -n spark-console create configmap spark-console-storage-wif \
  --from-file=config.json=external-account.json
```

Replace project numbers, pool/provider and audiences. For impersonation use the officially generated service_account_impersonation_url and corresponding IAM. The projected JWT audience must be allowed by the provider; Keycloak login tokens are not KSA WIF tokens. Prefix policies narrow the storage identity's privileges; they do not replace bucket IAM.

## Browsing and authorization

### Search within a prefix

Search file names or relative paths under the current prefix, including subfolders. Matching is case-insensitive and excludes file contents and unsaved draft folders. S3/MinIO and GCS scan metadata in bounded batches (100 objects by default, at most 500 per request). The result banner shows this batch's scanned/matched counts. Use **Next** to continue, even if a batch has no matches; only the last batch completes the scan. Clear the search to return to folder browsing. Navigating to another prefix resets the search. Authorization and cursor validation apply to every batch; search creates no audit rows.

### Draft folders

Use **New folder** to enter a relative directory name or nested path. The directory is a draft held only in the current page, with no empty objects or marker files written to S3/MinIO/GCS. Navigate back to see its Draft tag, or remove a draft without a storage operation. Upload a file or save a new text file inside it to make the prefix persistent. Empty drafts disappear on page reload or storage-root/account changes. Creating a draft requires list/upload permissions and a writable connection; actual file writes remain authorized by the plugin and audited normally. Draft creation/removal does not create audit records.

Administrators manage user grants in Object storage → Prefix permissions. The user selector lists active accounts across tenants and shows their tenant names. Accounts whose tenant has disabled object storage remain visible but cannot receive new grants until that permission is enabled. Administrators require explicit data grants too and can delegate only their own bucket/prefix/actions. Grants are bound to the recipient's actual tenant, not the administrator's tenant. Viewer file actions are determined by explicit grants, including upload/edit/delete/copy when authorized; only administrators can manage grants. Data access still requires the recipient's tenant module permission and matching personal grants. An empty prefix grants the entire bucket and requires confirmation; `team/a/` never matches `team/ab/`.

Tenant changes do not carry grants to the new tenant. Review and grant access for the new tenant explicitly; obsolete grants can be revoked even after the recipient is disabled or moved. Bootstrap grants remain one-time grants for their configured tenant, with revoked grants preserved across restarts. The periodic bootstrap reconciliation is separate from the user selector; refresh Prefix permissions after creating an account or changing tenant permissions rather than waiting for reconciliation.

Only authorized roots are shown. Directories are object-key prefixes, not filesystem directories. Pages contain up to 100 entries by default; cursors are bound to identity, directory and current grants. Dot path segments, backslashes, control characters and keys above 1024 bytes are rejected. Unicode, spaces and percent signs retain their raw key semantics. Revocation affects new requests, not necessarily streams already in progress.

`objectStorage.limits.textMaxBytes` controls the editable size and bounded preview (default 1048576 bytes, configurable from 1024 to 8388608). The server reads at most limit+1 bytes and trims an incomplete trailing UTF-8 character. Larger text files open as truncated read-only previews showing the full object size; download the remainder to view locally. Saving an oversized source is rejected by the server too. Increasing the limit requires memory/concurrency tuning; heavily JSON-escaped saves may need up to six times the text limit plus headers in the Nginx/Ingress body allowance. Binary, NUL and automatic decompression are rejected. HTML/SVG/Markdown are never rendered as executable content.

New text files and existing objects share a locally bundled Monaco editor: syntax colors, line numbers, folding, comments, undo, Ctrl+F search, Ctrl+H replace, case/whole-word/regex toggles, single/all replacement, Ctrl+D multi-selection, Alt+click and Ctrl+Alt+Up/Down multi-cursors. JSON/YAML/XML have structural diagnostics; JavaScript/TypeScript have syntax diagnostics. Other formats have highlighting/comment support, not a full compiler or remote language server. Supported extensions include md, txt, log, sh, bat, conf, cfg, py, java, xml, sql, hql, json, yml, yaml, ini, js, ts, csv, properties, toml, html and svg. New files require upload permission and conditional creation (no overwrite). Existing saves require edit permission and the original ETag/generation. File sizes use decimal B/KB/MB/GB/TB units (1 KB = 1000 B).

Uploads create new objects by default. The API supports explicit version-matched replacement, but the first UI has no force-overwrite action. Parts default to 8 MiB with four transfers per replica. Select the same name/size/lastModified file and choose Resume to reuse a locally stored, 24-hour encrypted ticket. Internal GCS session URLs are not exposed. Cancelling aborts unfinished transfers; the last GCS part may already have created an object, so refresh before assuming absence.

Copy is a server-side single-object operation within one connection, with a non-existing destination. Cross-connection/recursive operations are not supported. Delete affects one object. Older S3-compatible servers may ignore DeleteObject If-Match: the plugin checks the selected version first, but cannot guarantee an atomic delete against external concurrent writers on those servers. GCS uses generation conditions. Use underlying versioning/backups for stronger recovery; this UI does not manage historical object versions.

## Audits, memory and networking

- Persisted actions: UPLOAD, DOWNLOAD, CREATE, EDIT, DELETE, COPY and POLICY_CHANGE. Downloads always create one record, updated after streaming (succeeded/failed indicates server transfer completion, not proof of local disk persistence). The old audit.downloads setting no longer disables auditing. Browsing/previews/parts are not logged. Each upload updates one pending/succeeded/failed/cancelled/unknown row.
- File bytes are never stored in PostgreSQL. Plugin tables share the core database. A different database credential may be supplied but must access local_users; a completely isolated identity-less database is not supported in this first version.
- Records default to 30-day retention, cleaned in batches of at most 1000 per minute. Core Spark/workflow audit tables and permission records are not purged. Database failure blocks new mutations. Interrupted external operations can remain pending/unknown; there is no cross-system atomic transaction.
- Configure bucket lifecycle rules for abandoned S3 multipart uploads. Normal cancellation aborts them, but expired/lost tickets and crashes still need storage-side cleanup.
- The plugin uses its own ClusterIP/KSA without Kubernetes RBAC, and a read-only root filesystem. The gateway and plugin share a signing Secret of at least 32 bytes. Rotation invalidates old page/upload tickets; finish or cancel uploads first.
- NetworkPolicy is optional and disabled by default. It is not required merely because workloads share an intranet. Existing namespace/cluster policies still apply. When enabled, plugin ingress admits this release's backend and egress must permit DNS, PostgreSQL, storage and Google metadata/STS endpoints. Port-only rules are not destination allowlists; CNI enforcement is required. See the [Kubernetes NetworkPolicy semantics](https://kubernetes.io/docs/concepts/services-networking/network-policies/) and [GKE WIF network requirements](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/network-policy).
- Nginx disables storage-path request/response buffering and limits each request to 17 MiB. Configure the external Ingress for at least that body size and sufficient timeouts. Transfers do not inherit the core JSON 15-second timeout.
- All three storage overlays include ingress-nginx annotations for a 17m body limit, disabled buffering and 600-second timeouts. Adapt them for other controllers and synchronize them when changing the plugin timeout.
- GOMEMLIMIT defaults to 180MiB, with a 256Mi container limit. Tune concurrency, part size and memory together. Plugin failures do not fail the core backend's readiness.

## Local validation and deployment acceptance

```bash
npm install
npm test
npm run build
node scripts/object-storage-local-test.mjs
QA_VERSION=1.5.2 node scripts/container-object-storage-smoke.mjs
node scripts/object-storage-helm-test.mjs
```

These scripts use isolated labeled PostgreSQL/MinIO containers, not a production cluster, and clean their own containers. Enable features.objectStorage=true in local browser config.js for Mock UI; Mock upload simulates text up to 1 MiB, while larger transfers need the plugin. Browser checks use `node scripts/visual-qa-object-storage.mjs` after starting `npm run dev -- --host 127.0.0.1`.

Local passing tests are not real GCS WIF or production acceptance. Validate login/tenant mappings, CA/network/IAM, viewer and unauthorized paths, large-transfer memory, replacement recovery, save conflicts, retention and disabled-mode regression in your environment. Use an isolated bucket/prefix for destructive tests.

### Direct GKE KSA authorization

With serviceAccount.create=true and an empty serviceAccount.name, the chart creates its dedicated KSA automatically. For release spark-console in namespace spark-console, its name is spark-console-spark-control-center-object-storage. No Google service-account impersonation annotation is required for the direct principal binding below. Enable WIF on the cluster and node pool, then grant bucket access:

```bash
gcloud storage buckets add-iam-policy-binding \
  gs://<GCS_BUCKET> \
  --role=roles/storage.objectUser \
  --member="principal://iam.googleapis.com/projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/<PROJECT_ID>.svc.id.goog/subject/ns/spark-console/sa/spark-console-spark-control-center-object-storage" \
  --condition=None
```

Use the actual namespace/KSA if your release name differs. Repeat for each configured GCS bucket. IAM access and UI prefix grants are both required. With strict NetworkPolicy, GKE Dataplane V2 uses 169.254.169.254:80; other supported GKE setups use 169.254.169.252:988 (987 may also be needed). Follow the documentation for your GKE version rather than assuming an intranet port allowlist covers WIF.
