import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { parseAllDocuments } from "yaml";

const helm =
  process.env.HELM ||
  (process.platform === "win32" && existsSync(".tools/windows-amd64/helm.exe")
    ? resolve(".tools/windows-amd64/helm.exe")
    : "helm");
const chart = "charts/spark-control-center";
const production = `${chart}/examples/values-production.yaml`;
const run = (...args) =>
  execFileSync(helm, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
const manifests = (...args) =>
  parseAllDocuments(
    run(
      "template",
      "spark-console",
      chart,
      "--namespace",
      "spark-console",
      ...args,
    ),
    { version: "1.1" },
  )
    .map((d) => {
      assert.equal(d.errors.length, 0);
      return d.toJS();
    })
    .filter(Boolean);
function assertAnnotationStrings(value, path = "manifest") {
  if (!value || typeof value !== "object") return;
  if (value.annotations && typeof value.annotations === "object") {
    for (const [key, entry] of Object.entries(value.annotations))
      assert.equal(
        typeof entry,
        "string",
        `${path}.annotations.${key} must be a string`,
      );
  }
  for (const [key, child] of Object.entries(value))
    assertAnnotationStrings(child, `${path}.${key}`);
}
const isPlugin = (x) => x.metadata?.name?.endsWith("-object-storage");
const standalone = `${chart}/examples/values-production-s3.yaml`;
run("lint", chart, "-f", standalone);
const standaloneDocs = manifests("-f", standalone);
standaloneDocs.forEach((x) => assertAnnotationStrings(x));
assert(standaloneDocs.some((x) => isPlugin(x) && x.kind === "Deployment"));
assert(
  standaloneDocs.some(
    (x) =>
      x.kind === "Ingress" &&
      x.metadata.annotations["nginx.ingress.kubernetes.io/proxy-body-size"] ===
        "17m",
  ),
);
const standaloneBackend = standaloneDocs.find(
  (x) => x.kind === "Deployment" && x.metadata.name.endsWith("-backend"),
);
assert(standaloneBackend);
assert(
  !standaloneBackend.spec.template.spec.containers[0].env.some(
    (e) => e.name === "AUTH_INITIAL_ADMIN_PASSWORD",
  ),
  "OIDC-only installation must not require an initial administrator Secret",
);
const standaloneConfiguration = JSON.parse(
  standaloneDocs.find((x) => x.kind === "ConfigMap" && isPlugin(x)).data[
    "config.json"
  ],
);
assert.equal(standaloneConfiguration.connections[0].provider, "s3");
const annotationDocs = manifests(
  "-f",
  standalone,
  "-f",
  "scripts/fixtures/helm-annotations.yaml",
);
annotationDocs.forEach((x) => assertAnnotationStrings(x));
const annotationIngress = annotationDocs.find((x) => x.kind === "Ingress");
for (const keyword of ["off", "on", "true", "false", "yes", "no"])
  assert.equal(
    annotationIngress.metadata.annotations[`qa.example/${keyword}`],
    keyword,
  );
assert.equal(
  annotationIngress.metadata.annotations["qa.example/number"],
  "600",
);
assert.equal(
  annotationIngress.metadata.annotations[
    "nginx.ingress.kubernetes.io/proxy-buffering"
  ],
  "off",
);
assert.equal(
  annotationIngress.metadata.annotations[
    "nginx.ingress.kubernetes.io/proxy-request-buffering"
  ],
  "off",
);
assert.throws(
  () =>
    run(
      "template",
      "spark-console",
      chart,
      "-f",
      standalone,
      "-f",
      "scripts/fixtures/helm-annotations-invalid.yaml",
    ),
  /Invalid type|must be a string|got [^,\n]+, want string/,
);
const annotationPaths = [
  "ingress.annotations",
  "service.annotations",
  "serviceAccount.annotations",
  "podAnnotations",
  "backend.podAnnotations",
  "backend.serviceAccount.annotations",
  "objectStorage.serviceAccount.annotations",
];
for (const path of annotationPaths)
  for (const value of ["false", "600"])
    assert.throws(
      () =>
        run(
          "template",
          "spark-console",
          chart,
          "-f",
          standalone,
          "--set",
          `${path}.qa=${value}`,
        ),
      /Invalid type|must be a string|got [^,\n]+, want string/,
    );
if (run("template", "--help").includes("--skip-schema-validation"))
  assert.throws(
    () =>
      run(
        "template",
        "spark-console",
        chart,
        "-f",
        standalone,
        "-f",
        "scripts/fixtures/helm-annotations-invalid.yaml",
        "--skip-schema-validation",
      ),
    /metadata annotation.*must be a string/,
  );
for (const files of [[], ["-f", production]]) {
  run("lint", chart, ...files);
  const docs = manifests(...files);
  docs.forEach((x) => assertAnnotationStrings(x));
  assert.equal(
    docs.filter(isPlugin).length,
    0,
    "disabled plugin must not create any resources",
  );
  assert(
    !JSON.stringify(docs).includes("spark-control-center-object-storage:"),
    "disabled plugin must not pull its image",
  );
}
for (const provider of ["s3", "gke", "wif"]) {
  const flags = [
    "-f",
    production,
    "-f",
    `${chart}/examples/values-object-storage-${provider}.yaml`,
  ];
  run("lint", chart, ...flags);
  const docs = manifests(...flags);
  docs.forEach((x) => assertAnnotationStrings(x));
  const deployment = docs.find((x) => isPlugin(x) && x.kind === "Deployment");
  assert(deployment);
  assert.equal(
    deployment.spec.template.spec.automountServiceAccountToken,
    false,
  );
  assert.equal(
    deployment.spec.template.spec.containers[0].securityContext
      .readOnlyRootFilesystem,
    true,
  );
  assert(
    !docs.some(
      (x) =>
        isPlugin(x) &&
        ["Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"].includes(
          x.kind,
        ),
    ),
    "plugin must not inherit Kubernetes RBAC",
  );
  const cfg = docs.find((x) => isPlugin(x) && x.kind === "ConfigMap");
  const settings = JSON.parse(cfg.data["config.json"]);
  assert(settings.connections.length);
  if (provider === "wif")
    assert(
      deployment.spec.template.spec.volumes.some((x) =>
        x.projected?.sources.some((s) => s.serviceAccountToken?.audience),
      ),
    );
  if (provider === "s3") {
    assert(deployment.spec.template.spec.volumes.some((x) => x.secret));
    assert(!JSON.stringify(cfg).includes("secret-key-value"));
  }
}
const s3 = [
  "-f",
  production,
  "-f",
  `${chart}/examples/values-object-storage-s3.yaml`,
];
const configurable = manifests(
  ...s3,
  "--set",
  "objectStorage.limits.textMaxBytes=2097152",
);
const configured = JSON.parse(
  configurable.find((x) => isPlugin(x) && x.kind === "ConfigMap").data[
    "config.json"
  ],
);
assert.equal(configured.textMaxBytes, 2097152);
assert(!Object.hasOwn(configured, "downloadAudit"));
for (const flags of [
  ["--set", "objectStorage.internalAuth.existingSecret="],
  ["--set", "objectStorage.connections[0].s3.credentialsSecret="],
  ["--set", "objectStorage.connections[0].gcs.authMode=gkeWif"],
  ["--set", "objectStorage.limits.textMaxBytes=8388609"],
  ["--set", "objectStorage.limits.textMaxBytes=1023"],
  ["--set", "backend.enabled=false"],
  ["--set", "runtimeConfig.dataMode=mock"],
]) {
  assert.throws(
    () => run("template", "spark-console", chart, ...s3, ...flags),
    `invalid configuration accepted: ${flags.join(" ")}`,
  );
}
mkdirSync(".qa", { recursive: true });
console.log(run("package", chart, "--destination", ".qa").trim());
const packagedValues = run('show', 'values', '.qa/spark-control-center-1.5.2.tgz');
assert.match(packagedValues, /dataMode: api/);
assert.match(packagedValues, /mode: local/);
assert(!packagedValues.includes('Asia/Shanghai'));
assert(!packagedValues.includes('automatic.local'));
assert(!/10\.0\.32\./.test(packagedValues));
assert(packagedValues.indexOf('objectStorage:') > packagedValues.indexOf('backend:'));
console.log(
  "PASS: disabled/default and S3/GKE/generic WIF Helm resources, quoted annotation types and YAML 1.1 keywords, invalid annotation rejection, independent KSA, secret references and chart package.",
);
