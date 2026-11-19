import { App, Button, Card, Dropdown, Empty, Space, Spin, Typography } from "antd";
import {
  DatabaseOutlined, FolderOutlined, RightOutlined, TableOutlined, EyeOutlined,
} from "@ant-design/icons";
import { useCallback, useEffect, useRef, useState } from "react";
import type { CSSProperties, PointerEvent as ReactPointerEvent } from "react";
import { apiUrl } from "../runtimeConfig";

export async function sqlRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(apiUrl("/v1/sql" + path), { ...options, credentials: "same-origin" });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.message ?? response.statusText);
  return data as T;
}

type Kind = "catalog" | "namespace" | "table" | "view";
type CatalogNode = { name: string; kind: Kind };
type Schema = { "schema-id": number; fields: { name: string; type: unknown; required: boolean }[] };
type Metadata = {
  "current-schema-id"?: number;
  "current-version-id"?: number;
  versions?: { "version-id": number; "schema-id": number }[];
  comment?: string;
  schemas: Schema[];
};
type Generate = "SELECT" | "INSERT" | "UPDATE" | "DELETE" | "MERGE" | "DDL";
const generated: Generate[] = ["SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "DDL"];
const icons = { catalog: DatabaseOutlined, namespace: FolderOutlined, table: TableOutlined, view: EyeOutlined };
const catalog: CatalogNode = { name: "polaris", kind: "catalog" };
const key = (node: CatalogNode) => node.kind + ":" + node.name;
const branch = (node: CatalogNode) => node.kind === "catalog" || node.kind === "namespace";
const quote = (identifier: string) => "`" + identifier.replaceAll("`", "``") + "`";
export const quoteRelation = (name: string) => name.split(".").map(quote).join(".");
export const SQL_RELATION_DRAG_TYPE = "application/x-spark-console-relation";

function schemaFields(data: Metadata) {
  const id = data["current-schema-id"] ?? data.versions?.find((v) => v["version-id"] === data["current-version-id"])?.["schema-id"];
  return data.schemas?.find((schema) => schema["schema-id"] === id)?.fields ?? [];
}

function template(kind: Generate, name: string, fields: Schema["fields"]) {
  const relation = quoteRelation(name);
  const columns = fields.map((field) => quote(field.name));
  const first = columns[0];
  switch (kind) {
    case "SELECT": return `SELECT ${columns.join(", ") || "*"}\nFROM ${relation}\nLIMIT 100;`;
    case "INSERT": return `INSERT INTO ${relation} (${columns.join(", ")})\nVALUES (${columns.map(() => "NULL").join(", ")});`;
    case "UPDATE": return `UPDATE ${relation}\nSET ${first} = ${first}\nWHERE FALSE;`;
    case "DELETE": return `DELETE FROM ${relation}\nWHERE FALSE;`;
    case "MERGE": return `MERGE INTO ${relation} AS target\nUSING (SELECT ${columns.join(", ")} FROM ${relation} WHERE FALSE) AS source\n` +
      `ON target.${first} = source.${first}\nWHEN MATCHED THEN UPDATE SET ${first} = source.${first}\n` +
      `WHEN NOT MATCHED THEN INSERT (${columns.join(", ")})\nVALUES (${columns.map((column) => "source." + column).join(", ")});`;
    case "DDL": return `SHOW CREATE TABLE ${relation};`;
  }
}

function loadMetadata(node: CatalogNode, signal?: AbortSignal) {
  return sqlRequest<Metadata>(`/${node.kind}?` + new URLSearchParams({ name: node.name }), { signal });
}

const widthKey = "spark-console-sql-catalog-width";
const clampWidth = (width: number) => Math.max(210, Math.min(560, width));

export function useSqlCatalogWidth() {
  const [width, setWidth] = useState(() => clampWidth(Number(localStorage.getItem(widthKey)) || 260));
  useEffect(() => { localStorage.setItem(widthKey, String(width)); }, [width]);
  return [width, setWidth] as const;
}

export default function SqlCatalog({ en, width, resize, changed, onInsert }: {
  en: boolean;
  width: number;
  resize: (width: number) => void;
  changed?: { name: string } | { branches: string[] };
  onInsert: (text: string) => void;
}) {
  const { message } = App.useApp();
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([key(catalog)]));
  const [children, setChildren] = useState<Record<string, CatalogNode[]>>({});
  const [loading, setLoading] = useState<Set<string>>(() => new Set());
  const [selected, setSelected] = useState<CatalogNode>();
  const [metadata, setMetadata] = useState<Metadata>();
  const [metadataVersion, refreshMetadata] = useState(0);
  const [contextMenu, setContextMenu] = useState<string>();
  const pending = useRef(new Map<string, AbortController>());
  const drag = useRef<{ x: number; width: number }>();
  const labels = { catalog: en ? "Catalog" : "目录", namespace: en ? "Namespace" : "命名空间",
    table: en ? "Table" : "表", view: en ? "View" : "视图" };
  const error = useCallback((cause: unknown) => {
    void message.error(cause instanceof Error ? cause.message : String(cause));
  }, [message]);
  const load = useCallback(async (node: CatalogNode) => {
    const id = key(node);
    pending.current.get(id)?.abort();
    const controller = new AbortController();
    pending.current.set(id, controller);
    setLoading((old) => new Set(old).add(id));
    const options = { signal: controller.signal };
    const parameter = new URLSearchParams({ namespace: node.name });
    const sources = [sqlRequest<string[] | null>("/namespaces" +
      (node.kind === "namespace" ? "?" + new URLSearchParams({ parent: node.name }) : ""), options)
      .then((items) => (items ?? []).map((name): CatalogNode => ({ kind: "namespace", name })))];
    if (node.kind === "namespace") {
      for (const kind of ["table", "view"] as const) {
        sources.push(sqlRequest<{ table: string }[] | null>(`/${kind}s?${parameter}`, options)
          .then((items) => (items ?? []).map(({ table }): CatalogNode => ({ kind, name: table }))));
      }
    }
    const groups = await Promise.allSettled(sources);
    if (controller.signal.aborted) return;
    pending.current.delete(id);
    setLoading((old) => {
      const next = new Set(old);
      next.delete(id);
      return next;
    });
    setChildren((old) => ({ ...old, [id]: groups.flatMap((group) => group.status === "fulfilled" ? group.value : []) }));
    for (const group of groups) if (group.status === "rejected") error(group.reason);
  }, [error]);

  useEffect(() => {
    const active = pending.current;
    void load(catalog);
    return () => { active.forEach((controller) => controller.abort()); active.clear(); };
  }, [load]);
  useEffect(() => {
    if (!changed) return;
    const branches = "branches" in changed ? changed.branches : [changed.name.slice(0, changed.name.lastIndexOf("."))];
    for (const parent of branches) {
      const node: CatalogNode = parent === "polaris" ? catalog : { name: parent, kind: "namespace" };
      if (expanded.has(key(node))) void load(node);
    }
    if (selected?.name && branches.includes(selected.name.slice(0, selected.name.lastIndexOf(".")))) {
      refreshMetadata((value) => value + 1);
    }
    // React once to a completed mutation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changed]);
  useEffect(() => {
    setMetadata(undefined);
    if (!selected || branch(selected)) return;
    const controller = new AbortController();
    void loadMetadata(selected, controller.signal)
      .then((data) => { if (!controller.signal.aborted) setMetadata(data); })
      .catch((cause) => { if (!controller.signal.aborted) error(cause); });
    return () => controller.abort();
  }, [selected, metadataVersion, error]);

  function open(node: CatalogNode) {
    setSelected(node);
    if (!branch(node)) return;
    const id = key(node);
    setExpanded((old) => new Set(old).add(id));
    if (!children[id] && !loading.has(id)) void load(node);
  }

  function toggle(node: CatalogNode) {
    const id = key(node);
    if (!expanded.has(id)) open(node);
    else setExpanded((old) => {
      const next = new Set(old);
      next.delete(id);
      return next;
    });
  }

  async function generate(node: CatalogNode, kind: Generate) {
    try {
      if (kind === "DDL") {
        const sql = template(kind, node.name, []);
        const submitted = await sqlRequest<{ id: string }>("/queries", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sql, requestId: crypto.randomUUID() }),
        });
        for (let attempt = 0; attempt < 360; attempt++) {
          const result = await sqlRequest<{ state: string; pages: number; message?: string }>(`/queries/${submitted.id}`);
          if (result.state === "succeeded" && result.pages > 0) {
            const data = await sqlRequest<{ rows: unknown[][] }>(`/queries/${submitted.id}/pages/1`);
            const ddl = data.rows[0]?.[0];
            if (typeof ddl === "string" && ddl.trim()) {
              onInsert(ddl);
              return;
            }
          }
          if (result.state !== "queued" && result.state !== "running") {
            throw new Error(result.message || (en ? "Table definition unavailable" : "无法读取表定义"));
          }
          await new Promise((resolve) => window.setTimeout(resolve, 1000));
        }
        throw new Error(en ? "Table definition request timed out" : "读取表定义超时");
      }
      const fields = schemaFields(await loadMetadata(node));
      if (!fields.length) throw new Error(en ? "Table fields unavailable" : "无法读取表字段");
      onInsert(template(kind, node.name, fields));
    } catch (cause) { error(cause); }
  }

  function renderNode(node: CatalogNode, depth: number) {
    const id = key(node);
    const Icon = icons[node.kind];
    const isBranch = branch(node);
    const isOpen = expanded.has(id);
    const shortName = node.name.split(".").at(-1);
    return <div key={id} role="treeitem" aria-expanded={isBranch ? isOpen : undefined} aria-selected={key(selected ?? catalog) === id}>
      <Dropdown trigger={["contextMenu"]} open={contextMenu === id}
        onOpenChange={(open) => setContextMenu((current) => open ? id : current === id ? undefined : current)}
        menu={{ onClick: () => setContextMenu(undefined), items: [
        { key: "open", label: en ? "Open" : "打开", onClick: () => open(node) },
        { key: "refresh", label: en ? "Refresh" : "刷新", onClick: () => {
          if (isBranch) { open(node); if (children[id] && !loading.has(id)) void load(node); }
          else { open(node); refreshMetadata((value) => value + 1); }
        } },
        ...(node.kind === "table" ? [{ key: "generate", label: en ? "Generate SQL" : "生成 SQL",
          children: generated.map((kind) => ({ key: kind, label: kind, onClick: () => void generate(node, kind) })),
        }] : []),
      ] }}>
        <div className={`sql-catalog-row${selected && key(selected) === id ? " selected" : ""}`} style={{ "--depth": depth } as CSSProperties}>
          {isBranch ? <button className="sql-branch-toggle" aria-label={`${isOpen ? (en ? "Collapse" : "折叠") : (en ? "Expand" : "展开")} ${shortName}`}
            onClick={() => toggle(node)}><RightOutlined rotate={isOpen ? 90 : 0} /></button> : <span className="sql-branch-toggle" />}
          <button className="sql-node-name" title={node.name} onClick={() => open(node)} draggable={node.kind === "table"}
            aria-label={`${labels[node.kind]} ${node.name}`}
            onDragStart={(event) => {
              event.dataTransfer.effectAllowed = "copy";
              event.dataTransfer.setData(SQL_RELATION_DRAG_TYPE, quoteRelation(node.name));
            }}>
            <Icon aria-hidden />
            <span>{shortName}</span>
            {loading.has(id) && <Spin size="small" />}
          </button>
        </div>
      </Dropdown>
      {isBranch && isOpen && <div role="group">{children[id]?.map((item) => renderNode(item, depth + 1))}</div>}
    </div>;
  }

  function onPointerMove(event: ReactPointerEvent<HTMLDivElement>) {
    if (drag.current) resize(clampWidth(drag.current.width + event.clientX - drag.current.x));
  }

  const fields = metadata ? schemaFields(metadata) : [];
  return <Card className="sql-catalog" size="small" title={en ? "Metadata" : "元数据"}
    extra={<Button size="small" onClick={() => void load(catalog)}>{en ? "Refresh" : "刷新"}</Button>}>
    <div role="tree" aria-label={en ? "Polaris catalog" : "Polaris 目录"}>{renderNode(catalog, 0)}</div>
    {children[key(catalog)]?.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE}
      description={en ? "No visible namespaces" : "暂无可见命名空间"} />}
    {metadata && selected && <div className="sql-schema">
      <Typography.Text ellipsis={{ tooltip: selected.name }} strong>{selected.name}</Typography.Text>
      {metadata.comment && <Typography.Paragraph type="secondary">{metadata.comment}</Typography.Paragraph>}
      {fields.map((field) => <Space className="sql-schema-field" key={field.name}>
        <Typography.Text ellipsis={{ tooltip: field.name }}>{field.name}</Typography.Text>
        <Typography.Text ellipsis={{ tooltip: true }} type="secondary">{typeof field.type === "string" ? field.type : JSON.stringify(field.type)}</Typography.Text>
      </Space>)}
    </div>}
    <div className="sql-catalog-resize" role="separator" tabIndex={0} aria-orientation="vertical"
      aria-label={en ? "Resize metadata sidebar" : "调整元数据侧栏宽度"} aria-valuemin={210} aria-valuemax={560} aria-valuenow={width}
      onPointerDown={(event) => {
        drag.current = { x: event.clientX, width };
        event.currentTarget.setPointerCapture(event.pointerId);
      }} onPointerMove={onPointerMove} onLostPointerCapture={() => { drag.current = undefined; }}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
          event.preventDefault();
          resize(clampWidth(width + (event.key === "ArrowLeft" ? -12 : 12)));
        }
      }} />
  </Card>;
}
