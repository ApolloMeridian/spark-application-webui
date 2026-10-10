import { apiUrl, runtimeConfig } from "./runtimeConfig";
import type { Session } from "./auth";

export const TEXT_LIMIT = 1048576;
export const textExtensions = [
  "md",
  "txt",
  "log",
  "sh",
  "bat",
  "conf",
  "cfg",
  "py",
  "java",
  "xml",
  "sql",
  "hql",
  "json",
  "yml",
  "yaml",
  "ini",
  "js",
  "ts",
  "csv",
  "properties",
  "toml",
  "html",
  "svg",
];
export function isStorageText(key: string) {
  return textExtensions.includes(key.split(".").pop()?.toLowerCase() || "");
}
/** Decimal SI file sizes: KB=1000 B, MB=1000 KB. */
export function formatStorageSize(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const index =
    bytes === 0
      ? 0
      : Math.min(
          Math.floor(Math.log(bytes) / Math.log(1000)),
          units.length - 1,
        );
  return `${index ? (bytes / 1000 ** index).toFixed(2) : Math.round(bytes)} ${units[index]}`;
}
export interface StorageText {
  content: string;
  object: StorageObject;
  truncated: boolean;
  readOnly: boolean;
  previewBytes: number;
}
export const storageActions = [
  "list",
  "read",
  "download",
  "upload",
  "edit",
  "delete",
  "copy-source",
  "copy-destination",
] as const;
export type StorageAction = (typeof storageActions)[number];
export interface StorageRoot {
  bucket: string;
  prefix: string;
  actions: StorageAction[];
}
export interface StorageConnection {
  id: string;
  name: string;
  provider: "s3" | "gcs";
  editable: boolean;
  roots: StorageRoot[];
}
export interface StorageInfo {
  connections: StorageConnection[];
  textMaxBytes: number;
  maxUploadBytes: number;
  maxDownloadBytes: number;
  partSizeBytes: number;
}
export interface StorageObject {
  key: string;
  size: number;
  version: string;
  modified?: string;
  directory: boolean;
}
export interface StorageTarget {
  connection: string;
  bucket: string;
  key: string;
}
export interface StorageGrant {
  id: string;
  userId: string;
  tenantId: string;
  connection: string;
  bucket: string;
  prefix: string;
  actions: StorageAction[];
}
export interface StorageOperation {
  id: string;
  user: string;
  action: string;
  connection: string;
  bucket: string;
  key: string;
  target: string;
  result: string;
  created: string;
}
export interface PolicyInfo {
  grants: StorageGrant[];
  users: { id: string; username: string; role: string }[];
}
export function validStorageKey(key: string) {
  return (
    new TextEncoder().encode(key).length <= 1024 &&
    !key.startsWith("/") &&
    !/[\\\u0000-\u001f\u007f]/u.test(key) &&
    !key.split("/").some((v) => v === "." || v === "..")
  );
}
export function storageAllowed(
  session: Session | null,
  c: StorageConnection | undefined,
  bucket: string,
  key: string,
  action: StorageAction,
) {
  return (
    !!session &&
    validStorageKey(key) &&
    !!c?.roots.some(
      (root) =>
        root.bucket === bucket &&
        key.startsWith(root.prefix) &&
        root.actions.includes(action),
    )
  );
}
export class StorageError extends Error {
  constructor(public code: string) {
    super(code);
  }
}
const query = (t: StorageTarget) =>
  new URLSearchParams(t as unknown as Record<string, string>);
async function request<T>(
  path: string,
  init: RequestInit = {},
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(apiUrl("/v1/object-storage" + path), {
    ...init,
    credentials: "include",
    signal,
    headers: {
      Accept: "application/json",
      ...(init.body && typeof init.body === "string"
        ? { "Content-Type": "application/json" }
        : {}),
      ...init.headers,
    },
  });
  if (response.status === 401)
    window.dispatchEvent(new Event("spark-console:unauthorized"));
  if (!response.ok) {
    const body = await response
      .json()
      .catch(() => ({ code: "storage_unavailable" }));
    throw new StorageError(body.code || body.message || "storage_unavailable");
  }
  return response.json() as Promise<T>;
}
const apiService = {
  info: () => request<StorageInfo>("/connections"),
  list: (t: StorageTarget, cursor = "") =>
    request<{ objects: StorageObject[]; next: string }>(
      `/objects?${query(t)}&cursor=${encodeURIComponent(cursor)}`,
    ),
  text: (t: StorageTarget) => request<StorageText>(`/text?${query(t)}`),
  create: (t: StorageTarget, content: string) =>
    request<StorageObject>(`/text?${query(t)}`, {
      method: "POST",
      body: JSON.stringify({ content, version: "" }),
    }),
  save: (t: StorageTarget, content: string, version: string) =>
    request<StorageObject>(`/text?${query(t)}`, {
      method: "PUT",
      body: JSON.stringify({ content, version }),
    }),
  delete: (t: StorageTarget, version: string) =>
    request(`/objects?${query(t)}&version=${encodeURIComponent(version)}`, {
      method: "DELETE",
    }),
  copy: (t: StorageTarget, bucket: string, key: string, version: string) =>
    request(`/copy?${query(t)}`, {
      method: "POST",
      body: JSON.stringify({ bucket, key, version }),
    }),
  policies: () => request<PolicyInfo>("/policies"),
  saveGrant: (grant: Partial<StorageGrant>, confirmRoot: boolean) =>
    request("/policies", {
      method: "POST",
      body: JSON.stringify({ grant, confirmRoot }),
    }),
  deleteGrant: (id: string) =>
    request(`/policies/${encodeURIComponent(id)}`, { method: "DELETE" }),
  operations: () => request<StorageOperation[]>("/operations"),
  download: (t: StorageTarget) => {
    const link = document.createElement("a");
    link.href = apiUrl(`/v1/object-storage/download?${query(t)}`);
    link.download = t.key.split("/").pop() || "download";
    link.click();
  },
  upload: async (
    t: StorageTarget,
    file: File,
    onProgress: (n: number) => void,
    signal: AbortSignal,
    resume = false,
  ) => {
    const cacheKey = `spark-storage-upload:${current?.tenantId || "default"}:${current?.id || ""}:${JSON.stringify(t)}`;
    const old = JSON.parse(localStorage.getItem(cacheKey) || "null") as {
      ticket: string;
      name: string;
      size: number;
      lastModified: number;
    } | null;
    let ticket = "";
    let offset = 0;
    let chunk = 8388608;
    if (
      resume &&
      old &&
      old.name === file.name &&
      old.size === file.size &&
      old.lastModified === file.lastModified
    ) {
      ticket = old.ticket;
      const status = await request<{ offset: number; partSizeBytes: number }>(
        "/uploads",
        { headers: { "X-Storage-Upload": ticket } },
        signal,
      );
      offset = status.offset;
      chunk = status.partSizeBytes;
    } else {
      const started = await request<{
        ticket: string;
        complete?: boolean;
        partSizeBytes: number;
      }>(
        `/uploads?${query(t)}`,
        { method: "POST", body: JSON.stringify({ size: file.size }) },
        signal,
      );
      if (started.complete) {
        onProgress(100);
        return;
      }
      ticket = started.ticket;
      chunk = started.partSizeBytes;
      localStorage.setItem(
        cacheKey,
        JSON.stringify({
          ticket,
          name: file.name,
          size: file.size,
          lastModified: file.lastModified,
        }),
      );
    }
    onProgress(file.size ? (offset / file.size) * 100 : 100);
    try {
      while (offset < file.size) {
        const part = file.slice(offset, Math.min(offset + chunk, file.size));
        const next = await request<{ offset: number }>(
          `/uploads/part?offset=${offset}`,
          {
            method: "PUT",
            headers: {
              "X-Storage-Upload": ticket,
              "Content-Type": "application/octet-stream",
            },
            body: part,
          },
          signal,
        );
        offset = next.offset;
        onProgress((offset / file.size) * 100);
      }
      await request(
        "/uploads/complete",
        { method: "POST", headers: { "X-Storage-Upload": ticket } },
        signal,
      );
      localStorage.removeItem(cacheKey);
    } catch (e) {
      if (signal.aborted) {
        await request("/uploads", {
          method: "DELETE",
          headers: { "X-Storage-Upload": ticket },
        })
          .then(() => localStorage.removeItem(cacheKey))
          .catch(() => {});
      }
      throw e;
    }
  },
};

// Browser-local deterministic fixture; never used by the deployed plugin.
type Fixture = Record<
  string,
  { content: string; size: number; version: number }
>;
const seed: Fixture = {
  "demo/spark-artifacts/team/analytics/config.yaml": {
    content: "spark:\n  executor:\n    instances: 3\n    memory: 8g\n",
    size: 47,
    version: 1,
  },
  "demo/spark-artifacts/team/analytics/scripts/job.py": {
    content:
      "from pyspark.sql import SparkSession\n\nspark = SparkSession.builder.getOrCreate()\nprint(spark.range(10).count())\n",
    size: 111,
    version: 1,
  },
  "demo/spark-artifacts/team/analytics/large.log": {
    content: "log entry\n".repeat(209716),
    size: 2097152,
    version: 1,
  },
};
let fixture: Fixture =
  JSON.parse(localStorage.getItem("spark-storage-fixture") || "null") ||
  structuredClone(seed);
let audits: StorageOperation[] = [];
let grants: StorageGrant[] = [];
let current: Session | null = null;
const demo: StorageConnection = {
  id: "demo",
  name: "MinIO demo",
  provider: "s3",
  editable: true,
  roots: [
    {
      bucket: "spark-artifacts",
      prefix: "team/analytics/",
      actions: [...storageActions],
    },
  ],
};
const keyOf = (t: StorageTarget) => `${t.connection}/${t.bucket}/${t.key}`;
function check(t: StorageTarget, a: StorageAction) {
  if (
    !storageAllowed(current, mockConnection(), t.bucket, t.key, a) ||
    t.connection !== "demo"
  )
    throw new StorageError("access_denied");
}
function mockConnection(): StorageConnection {
  if (current?.role === "admin") return demo;
  const owned = grants.filter(
    (g) =>
      g.userId === current?.id &&
      g.tenantId === (current?.tenantId || "default"),
  );
  return {
    ...demo,
    roots: owned.length
      ? owned
      : demo.roots.map((r) => ({
          ...r,
          actions: ["list", "read", "download"],
        })),
  };
}
function saveFixture(t: StorageTarget, action: string, target = "") {
  localStorage.setItem("spark-storage-fixture", JSON.stringify(fixture));
  audits.unshift({
    id: crypto.randomUUID(),
    user: current?.username || "",
    action,
    connection: t.connection,
    bucket: t.bucket,
    key: t.key,
    target,
    result: "succeeded",
    created: new Date().toISOString(),
  });
}
function object(k: string, v: Fixture[string]): StorageObject {
  return {
    key: k,
    size: v.size,
    version: String(v.version),
    modified: "2026-10-09T00:00:00Z",
    directory: false,
  };
}
const mockService: typeof apiService = {
  info: async () => ({
    connections: [mockConnection()],
    textMaxBytes: TEXT_LIMIT,
    maxUploadBytes: 1073741824,
    maxDownloadBytes: 1073741824,
    partSizeBytes: 8388608,
  }),
  list: async (t) => {
    check(t, "list");
    const dirs = new Set<string>();
    const objects: StorageObject[] = [];
    for (const [k, v] of Object.entries(fixture)) {
      if (!k.startsWith(keyOf(t))) continue;
      const rest = k.slice(keyOf(t).length);
      if (rest.includes("/")) {
        dirs.add(t.key + rest.split("/")[0] + "/");
      } else {
        objects.push(object(t.key + rest, v));
      }
    }
    return {
      objects: [
        ...Array.from(dirs).map((k) => ({
          key: k,
          size: 0,
          version: "",
          modified: "",
          directory: true,
        })),
        ...objects,
      ],
      next: "",
    };
  },
  text: async (t) => {
    check(t, "read");
    const x = fixture[keyOf(t)];
    if (!x) throw new StorageError("object_not_found");
    if (!isStorageText(t.key)) throw new StorageError("not_text");
    const bytes = new TextEncoder().encode(x.content);
    const truncated = x.size > TEXT_LIMIT || bytes.length > TEXT_LIMIT;
    const content = new TextDecoder().decode(bytes.slice(0, TEXT_LIMIT));
    return {
      content,
      object: object(t.key, x),
      truncated,
      readOnly: truncated,
      previewBytes: new TextEncoder().encode(content).length,
    };
  },
  create: async (t, content) => {
    check(t, "upload");
    if (!isStorageText(t.key)) throw new StorageError("not_text");
    if (fixture[keyOf(t)]) throw new StorageError("version_conflict");
    const size = new TextEncoder().encode(content).length;
    if (size > TEXT_LIMIT) throw new StorageError("text_too_large");
    fixture[keyOf(t)] = { content, size, version: 1 };
    saveFixture(t, "CREATE");
    return object(t.key, fixture[keyOf(t)]);
  },
  save: async (t, content, version) => {
    check(t, "edit");
    const x = fixture[keyOf(t)];
    if (!x) throw new StorageError("object_not_found");
    if (x.size > TEXT_LIMIT) throw new StorageError("text_too_large");
    if (String(x.version) !== version)
      throw new StorageError("version_conflict");
    const size = new TextEncoder().encode(content).length;
    if (size > TEXT_LIMIT) throw new StorageError("text_too_large");
    fixture[keyOf(t)] = { content, size, version: x.version + 1 };
    saveFixture(t, "EDIT");
    return object(t.key, fixture[keyOf(t)]);
  },
  delete: async (t) => {
    check(t, "delete");
    delete fixture[keyOf(t)];
    saveFixture(t, "DELETE");
    return { deleted: true };
  },
  copy: async (t, bucket, key, version) => {
    check(t, "copy-source");
    const dest = { ...t, bucket, key };
    check(dest, "copy-destination");
    if (fixture[keyOf(dest)]) throw new StorageError("version_conflict");
    const x = fixture[keyOf(t)];
    if (!x || String(x.version) !== version)
      throw new StorageError("version_conflict");
    fixture[keyOf(dest)] = { ...x };
    saveFixture(t, "COPY", key);
    return object(key, x);
  },
  policies: async () => {
    if (current?.role !== "admin") throw new StorageError("access_denied");
    return {
      grants,
      users: [
        { id: current.id, username: current.username, role: "admin" },
        { id: "mock-viewer", username: "viewer", role: "viewer" },
      ],
    };
  },
  saveGrant: async (g, confirm) => {
    if (
      current?.role !== "admin" ||
      !validStorageKey(g.prefix || "") ||
      !g.prefix?.startsWith("team/analytics/")
    )
      throw new StorageError("access_denied");
    if (!g.prefix && !confirm)
      throw new StorageError("root_confirmation_required");
    grants = [
      ...grants.filter(
        (x) => !(x.userId === g.userId && x.prefix === g.prefix),
      ),
      {
        ...g,
        id: crypto.randomUUID(),
        tenantId: "default",
        key: "",
      } as StorageGrant,
    ];
    saveFixture(
      { connection: g.connection!, bucket: g.bucket!, key: g.prefix! },
      "POLICY_CHANGE",
    );
    return {};
  },
  deleteGrant: async (id) => {
    if (current?.role !== "admin") throw new StorageError("access_denied");
    grants = grants.filter((g) => g.id !== id);
    saveFixture(
      { connection: "demo", bucket: "spark-artifacts", key: "" },
      "POLICY_CHANGE",
    );
    return {};
  },
  operations: async () => audits,
  download: (t) => {
    check(t, "download");
    const x = fixture[keyOf(t)];
    if (!x) throw new StorageError("object_not_found");
    const url = URL.createObjectURL(new Blob([x.content]));
    const link = document.createElement("a");
    link.href = url;
    link.download = t.key.split("/").pop() || "";
    link.click();
    saveFixture(t, "DOWNLOAD");
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
  upload: async (t, file, onProgress, signal) => {
    check(t, "upload");
    if (fixture[keyOf(t)]) throw new StorageError("version_conflict");
    if (file.size > TEXT_LIMIT) throw new StorageError("mock_upload_limit");
    const content = await file.text();
    if (signal.aborted) throw new DOMException("Aborted", "AbortError");
    fixture[keyOf(t)] = { content, size: file.size, version: 1 };
    saveFixture(t, "UPLOAD");
    onProgress(100);
  },
};
export function storageService(session: Session | null) {
  current = session;
  return runtimeConfig.dataMode === "mock" ? mockService : apiService;
}
