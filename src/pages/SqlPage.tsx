import {
  App, Button, Card, Empty, Input, Popconfirm, Select, Space, Table, Tabs,
  Tag, Typography,
} from "antd";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { useAuth } from "../auth";
import SqlCatalog, { sqlRequest as request, useSqlCatalogWidth } from "../components/SqlCatalog";
import type { SqlEditorHandle } from "../components/SqlEditor";
import { useI18n } from "../i18n";
import { hasPermission } from "../permissions";
import { apiUrl } from "../runtimeConfig";
import { formatTimestamp } from "../utils";
import "./SqlPage.css";

interface Grant {
  userId: string;
  table: string;
  tableUuid: string;
  scope: "catalog" | "namespace" | "table" | "view";
  actions: string[];
}

interface Query {
  id: string;
  state: "queued" | "running" | "succeeded" | "failed" | "cancelled" | "interrupted";
  tables: string[] | null;
  columns: { columnName: string; dataType: string }[] | null;
  rowCount: number;
  pages: number;
  truncated: boolean;
  message?: string;
  createdAt: string;
  expiresAt?: string;
  resultState?: "none" | "available" | "expired" | "unavailable";
  writeTable?: string;
  ordinal: number;
  steps?: Query[];
  refreshBranches?: string[];
}

const SqlEditor = lazy(() => import("../components/SqlEditor"));
type GrantsInfo = { grants: Grant[]; users: { id: string; name: string; tenant: string }[]; catalogAdmin?: boolean };
const privilegeLabels = {
  read: ["SELECT", "读取数据"], write: ["Append rows", "追加数据"],
  overwrite: ["Overwrite", "覆盖数据"], update: ["Update rows", "更新行"],
  deleteRows: ["Delete rows", "删除行"], merge: ["Merge rows", "合并数据"],
  create: ["Create", "创建"], delete: ["Delete", "删除"],
  purge: ["Drop and purge", "删除表及底层数据"],
  metadataRead: ["Read metadata", "读取元数据"], metadataWrite: ["Edit metadata", "修改元数据"],
} as const;
const levels = Object.keys(privilegeLabels) as (keyof typeof privilegeLabels)[];
const availablePrivilege = (scope: Grant["scope"], action: string) =>
  ((scope !== "table" && scope !== "view") || action !== "create") &&
  (scope !== "view" || !["write", "overwrite", "update", "deleteRows", "merge", "purge"].includes(action));
const active = (query?: Query) => query?.state === "queued" || query?.state === "running";
const jsonBody = (method: string, body: object): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

const cell = (value: unknown) => typeof value === "string" ? value : JSON.stringify(value);

function HistoryStatement({ id, en }: { id: string; en: boolean }) {
  const [text, setText] = useState<string>();
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    void request<{ sql: string }>(`/queries/${id}/statement`, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setText(data.sql); })
      .catch(() => { if (!controller.signal.aborted) setFailed(true); });
    return () => controller.abort();
  }, [id, attempt]);
  if (failed) return <Button onClick={() => { setFailed(false); setAttempt(attempt + 1); }}>{en ? "Retry loading SQL" : "重试加载 SQL"}</Button>;
  if (text === undefined) return <Typography.Text type="secondary">{en ? "Loading SQL…" : "正在加载 SQL…"}</Typography.Text>;
  return text ? <pre className="sql-history-text">{text}</pre> :
    <Typography.Text type="secondary">{en ? "SQL text was not saved" : "未保存 SQL 文本"}</Typography.Text>;
}

export default function SqlPage() {
  const { locale } = useI18n();
  const en = locale === "en-US";
  const { message } = App.useApp();
  const { session } = useAuth();
  const manager = hasPermission(session, "sqlManage");
  const [sql, setSql] = useState("SELECT 1 AS result");
  const editor = useRef<SqlEditorHandle>(null);
  const [tab, setTab] = useState("result");
  const [catalogWidth, setCatalogWidth] = useSqlCatalogWidth();
  const [changed, setChanged] = useState<{ name: string } | { branches: string[] }>();
  const [queryID, setQueryID] = useState("");
  const [query, setQuery] = useState<Query>();
  const [stepID, selectStep] = useState("");
  const [stepResult, setStepResult] = useState<Query>();
  const [history, setHistory] = useState<Query[]>([]);
  const [historyPage, setHistoryPage] = useState(1);
  const [historyHasNext, setHistoryHasNext] = useState(false);
  const [historyBusy, setHistoryBusy] = useState(false);
  const [now, setNow] = useState(Date.now);
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<unknown[][]>([]);
  const [loading, setLoading] = useState(false);
  const [grants, setGrants] = useState<GrantsInfo>({ grants: [], users: [] });
  const [grantUser, setGrantUser] = useState<string>();
  const [grantTable, setGrantTable] = useState("");
  const [grantScope, setGrantScope] = useState<Grant["scope"]>("table");
  const [grantActions, setGrantActions] = useState<string[]>(["read", "metadataRead"]);
  const notify = useCallback((error: unknown) => {
    void message.error(error instanceof Error ? error.message : String(error));
  }, [message]);
  const refreshGrants = useCallback(async () => {
    setGrants(await request<GrantsInfo>("/grants"));
  }, []);
  useEffect(() => {
    if (tab === "grants" && manager) void refreshGrants().catch(notify);
  }, [tab, manager, notify, refreshGrants]);
  useEffect(() => {
    if (tab !== "history") return;
    const controller = new AbortController();
    setHistoryBusy(true);
    void request<{ queries: Query[]; hasNext: boolean }>(`/queries?page=${historyPage}`, { signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) return;
        setHistory(data.queries);
        setHistoryHasNext(data.hasNext);
      })
      .catch((error) => { if (!controller.signal.aborted) notify(error); })
      .finally(() => { if (!controller.signal.aborted) setHistoryBusy(false); });
    return () => controller.abort();
  }, [tab, historyPage, notify]);
  useEffect(() => {
    const deadlines = [query, stepResult, ...history].flatMap((item) => item?.resultState === "available" && item.expiresAt ? [Date.parse(item.expiresAt)] : []);
    const next = Math.min(...deadlines.filter((deadline) => deadline > now));
    if (!Number.isFinite(next)) return;
    const timer = window.setTimeout(() => setNow(Date.now()), Math.max(0, Math.min(next - Date.now(), 2_000_000_000)));
    return () => window.clearTimeout(timer);
  }, [query, stepResult, history, now]);
  useEffect(() => {
    setQuery(undefined);
    selectStep("");
    setStepResult(undefined);
    setPage(1);
    setRows([]);
    if (!queryID) return;
    const controller = new AbortController();
    const refreshed = new Set<string>();
    let timer: number;
    async function poll() {
      try {
        const next = await request<Query>("/queries/" + queryID, { signal: controller.signal });
        if (controller.signal.aborted) return;
        setQuery(next);
        const branches = [next, ...(next.steps ?? [])].flatMap((item) => {
          if (!item.refreshBranches?.length || refreshed.has(item.id)) return [];
          refreshed.add(item.id);
          return item.refreshBranches;
        });
        if (branches.length) setChanged({ branches: [...new Set(branches)] });
        if (active(next)) timer = window.setTimeout(() => void poll(), 1000);
      } catch (error) {
        if (!controller.signal.aborted) notify(error);
      }
    }
    void poll();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [queryID, notify]);
  useEffect(() => {
    setPage(1);
    setStepResult(undefined);
    if (!stepID) return;
    const controller = new AbortController();
    void request<Query>("/queries/" + stepID, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setStepResult(data); })
      .catch((error) => { if (!controller.signal.aborted) notify(error); });
    return () => controller.abort();
  }, [stepID, notify]);
  const displayed = stepID ? stepResult : query;
  const expired = (item: Query) => item.resultState === "expired" ||
    (item.resultState === "available" && !!item.expiresAt && Date.parse(item.expiresAt) <= now);
  const expiredLabel = en ? "Result expired" : "结果已过期";
  const queryExpired = !!displayed && expired(displayed);
  const resultId = displayed?.id;
  const pages = queryExpired ? 0 : displayed?.pages ?? 0;
  useEffect(() => {
    setRows([]);
    if (!resultId || !pages || page > pages) return;
    const controller = new AbortController();
    void request<{ rows: unknown[][] }>(`/queries/${resultId}/pages/${page}`, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setRows(data.rows); })
      .catch((error) => { if (!controller.signal.aborted) notify(error); });
    return () => controller.abort();
  }, [pages, page, resultId, notify]);

  async function submit() {
    const executionText = editor.current?.executionText() ?? sql;
    if (!executionText.trim() || loading) return;
    setLoading(true);
    try {
      const next = await request<{ id: string }>("/queries", jsonBody("POST", {
        sql: executionText, requestId: crypto.randomUUID(),
      }));
      setQueryID(next.id);
      setTab("result");
    } catch (error) {
      notify(error);
    } finally {
      setLoading(false);
    }
  }
  async function updateGrant(method: "PUT" | "DELETE", userId: string, scope: Grant["scope"], table: string, actions: string[]) {
    try {
      await request("/grants", jsonBody(method, { userId, scope, table, actions }));
      await refreshGrants();
    } catch (error) { notify(error); }
  }
  const stateLabels = {
    queued: en ? "Queued" : "排队中", running: en ? "Running" : "执行中",
    succeeded: en ? "Succeeded" : "成功", failed: en ? "Failed" : "失败",
    cancelled: en ? "Cancelled" : "已取消", interrupted: en ? "Interrupted" : "已中断",
  };
  const stateTag = (item: Query) => <Tag color={item.state === "succeeded" ? "green" : item.state === "failed" ? "red" : active(item) ? "blue" : "default"}>{stateLabels[item.state]}</Tag>;
  const tableLabel = en ? "Table" : "表";
  const result = query ? (
    <Space direction="vertical" className="sql-full-width">
      <Space wrap>{stateTag(query)}
        {query.message && <Typography.Text type="danger">{query.message}</Typography.Text>}
      </Space>
      {!!query.steps?.length && <Tabs size="small" activeKey={stepID} onChange={selectStep} items={query.steps.map((step) => ({
        key: step.id,
        disabled: active(step),
        label: <Space>{en ? `Statement ${step.ordinal}` : `语句 ${step.ordinal}`}{stateTag(step)}</Space>,
      }))} />}
      <Space wrap>
        {stepID && displayed?.message && <Typography.Text type="danger">{displayed.message}</Typography.Text>}
        {!queryExpired && displayed?.columns && <Button href={apiUrl(`/v1/sql/queries/${displayed.id}/download`)}>{en ? "Download CSV" : "下载 CSV"}</Button>}
        {displayed?.truncated && <Tag color="gold">{en ? "Result limit reached" : "已达结果上限"}</Tag>}
        {queryExpired && <Tag color="gold">{expiredLabel}</Tag>}
        {displayed?.resultState === "unavailable" && <Tag color="gold">{en ? "Result unavailable" : "结果不可访问"}</Tag>}
        {displayed?.state === "succeeded" && (displayed.writeTable || displayed.resultState !== "none") &&
          <Typography.Text type="secondary">{displayed.writeTable
            ? (en ? "Rows appended to " : "已追加数据到 ") + displayed.writeTable
            : `${displayed.rowCount.toLocaleString(locale)} ${en ? "rows stored" : "行已保存"}`}</Typography.Text>}
      </Space>
      {!queryExpired && displayed?.columns && <Table
        size="small" rowKey="id" pagination={false} scroll={{ x: "max-content" }}
        columns={displayed.columns.map((column, i) => ({
          key: i, title: column.columnName, render: (_, row) => cell(row.cells[i]),
        }))}
        dataSource={rows.map((cells, id) => ({ id, cells }))}
      />}
      {pages > 1 && <Space>
        <Button disabled={page <= 1} onClick={() => setPage(page - 1)}>{en ? "Previous" : "上一页"}</Button>
        <Select aria-label={en ? "Result page" : "结果页"} value={page} onChange={setPage} showSearch
          options={Array.from({ length: pages }, (_, index) => ({ value: index + 1, label: String(index + 1) }))} />
        <Button disabled={page >= pages} onClick={() => setPage(page + 1)}>{en ? "Next" : "下一页"}</Button>
      </Space>}
    </Space>
  ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={en ? "No query selected" : "尚未选择查询"} />;

  return <div className="sql-workspace">
    <Typography.Title level={2}>{en ? "SQL workspace" : "SQL 工作台"}</Typography.Title>
    <div className="sql-panels" style={{ "--sql-catalog-width": `${catalogWidth}px` } as CSSProperties}>
      <SqlCatalog en={en} width={catalogWidth} resize={setCatalogWidth} changed={changed} onInsert={setSql} />
      <div className="sql-main">
        <Card size="small" title={en ? "Spark SQL" : "Spark SQL 查询"}>
          <Space direction="vertical" className="sql-full-width">
            <Suspense fallback={<div className="sql-editor" aria-busy="true" />}>
              <SqlEditor ref={editor} value={sql} onChange={setSql} onRun={() => void submit()} />
            </Suspense>
            <Space>
              <Button type="primary" disabled={!sql.trim()} loading={loading} onClick={() => void submit()}>{en ? "Run query" : "运行查询"}</Button>
              {query && active(query) && <Button onClick={() => void request(`/queries/${query.id}/cancel`, jsonBody("POST", {})).catch(notify)}>{en ? "Cancel query" : "取消查询"}</Button>}
            </Space>
          </Space>
        </Card>
        <Card size="small">
          <Tabs activeKey={tab} onChange={setTab} items={[
            { key: "result", label: en ? "Results" : "查询结果", children: result },
            { key: "history", label: en ? "History" : "查询历史", children: <>
              <Table rowKey="id" size="small" dataSource={history} loading={historyBusy} pagination={false} scroll={{ x: "max-content" }}
              expandable={{ expandedRowRender: (item) => <HistoryStatement id={item.id} en={en} /> }}
              columns={[
                { title: en ? "Submitted" : "提交时间", render: (_, item) => formatTimestamp(item.createdAt) },
                { title: tableLabel, render: (_, item) => [item.writeTable, ...(item.tables ?? [])].filter(Boolean).join(", ") },
                { title: en ? "Status" : "状态", render: (_, item) => <>{stateTag(item)}{expired(item) && <Tag color="gold">{expiredLabel}</Tag>}</> },
                { title: en ? "Query" : "查询", render: (_, item) => <Button type="link" onClick={() => { setQueryID(item.id); setTab("result"); }}>{en ? "Open" : "打开"}</Button> },
              ]}
              />
              <Space className="sql-history-pagination">
                <Button disabled={historyBusy || historyPage === 1} onClick={() => setHistoryPage(historyPage - 1)}>{en ? "Previous" : "上一页"}</Button>
                <Typography.Text>{historyPage}</Typography.Text>
                <Button disabled={historyBusy || !historyHasNext} onClick={() => setHistoryPage(historyPage + 1)}>{en ? "Next" : "下一页"}</Button>
              </Space>
            </> },
            { key: "catalog", label: en ? "Catalog changes" : "目录操作", children: <CatalogChanges en={en} refresh={(name) => setChanged({ name })} notify={notify} /> },
            ...(manager ? [{ key: "grants", label: en ? "Table grants" : "表授权", children: <>
              <Space wrap className="sql-grant-form">
                <Select showSearch optionFilterProp="label" placeholder={en ? "Select a user" : "选择用户"}
                  value={grantUser} onChange={setGrantUser} options={grants.users.map(({ id, name, tenant }) => ({ value: id, label: `${name} (${tenant})` }))} />
                <Select value={grantScope} onChange={(value) => {
                  setGrantScope(value);
                  if (value === "catalog") setGrantTable("polaris");
                  setGrantActions((old) => old.filter((action) => availablePrivilege(value, action)));
                }} options={[{ value: "table", label: tableLabel }, { value: "view", label: en ? "View" : "视图" },
                  { value: "namespace", label: en ? "Namespace" : "命名空间" },
                  ...(grants.catalogAdmin ? [{ value: "catalog", label: en ? "Catalog" : "目录" }] : [])]} />
                <Input aria-label={en ? "Qualified resource" : "资源全名"} placeholder={grantScope === "namespace" ? "polaris.namespace" : "polaris.namespace." + grantScope}
                  disabled={grantScope === "catalog"} value={grantTable} onChange={(event) => setGrantTable(event.target.value)} />
                <Select mode="multiple" value={grantActions} onChange={setGrantActions}
                  aria-label={en ? "Privileges" : "权限"}
                  options={levels.filter((action) => availablePrivilege(grantScope, action))
                    .map((value) => ({ value, label: privilegeLabels[value][en ? 0 : 1] }))} />
                <Button type="primary" disabled={!grantUser || !grantTable.trim() || !grantActions.length} onClick={() => {
                  if (grantUser) void updateGrant("PUT", grantUser, grantScope, grantTable.trim(), grantActions);
                }}>{en ? "Save grant" : "保存授权"}</Button>
              </Space>
              <Table rowKey={(item) => item.userId + ":" + item.scope + ":" + item.table} size="small" dataSource={grants.grants} scroll={{ x: "max-content" }}
                pagination={{ pageSize: 10 }} columns={[
                  { title: en ? "User" : "用户", render: (_, item) => grants.users.find((user) => user.id === item.userId)?.name ?? item.userId },
                  { title: en ? "Tenant" : "租户", render: (_, item) => grants.users.find((user) => user.id === item.userId)?.tenant ?? "—" },
                  { title: en ? "Resource" : "资源", dataIndex: "table" },
                  { title: en ? "Privileges" : "权限", render: (_, item) => item.actions.map((action) => privilegeLabels[action as keyof typeof privilegeLabels]?.[en ? 0 : 1] ?? action).join(", ") },
                  { title: en ? "Actions" : "操作", render: (_, item) => (item.scope !== "catalog" || grants.catalogAdmin) && <Space>
                    <Button type="link" onClick={() => { setGrantUser(item.userId); setGrantTable(item.table); setGrantScope(item.scope); setGrantActions(item.actions); }}>{en ? "Edit" : "编辑"}</Button>
                    <Popconfirm title={en ? "Revoke this grant?" : "撤销该资源授权？"} onConfirm={() => updateGrant("DELETE", item.userId, item.scope, item.table, [])}>
                      <Button danger type="link">{en ? "Revoke" : "撤销"}</Button>
                    </Popconfirm>
                  </Space> },
                ]} />
            </> }] : []),
          ]} />
        </Card>
      </div>
    </div>
  </div>;
}

function CatalogChanges({ en, refresh, notify }: { en: boolean; refresh: (name: string) => void; notify: (error: unknown) => void }) {
  const { message } = App.useApp();
  const [scope, setScope] = useState<"namespace" | "table">("namespace");
  const [operation, setOperation] = useState("create");
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [fields, setFields] = useState("id: long");
  const [busy, setBusy] = useState(false);
  async function apply() {
    setBusy(true);
    try {
      await request("/catalog", jsonBody("POST", {
        operation, scope, name: name.trim(), comment, fields: scope === "table" && operation === "create" ?
          fields.split("\n").filter((line) => line.trim()).map((line) => {
            const [name, type] = line.split(":", 2).map((part) => part.trim());
            return { name, type };
          }) : [],
      }));
      refresh(name.trim());
      void message.success(en ? "Catalog updated" : "目录已更新");
    } catch (error) { notify(error); }
    finally { setBusy(false); }
  }
  return <Space direction="vertical" className="sql-full-width">
    <Space wrap>
      <Select value={scope} onChange={setScope} options={[
        { value: "namespace", label: en ? "Namespace" : "命名空间" },
        { value: "table", label: en ? "Table" : "表" },
      ]} />
      <Select value={operation} onChange={setOperation} options={[
        { value: "create", label: en ? "Create" : "创建" },
        { value: "comment", label: en ? "Edit comment" : "编辑备注" },
        { value: "delete", label: en ? "Remove from catalog" : "从目录移除" },
      ]} />
    </Space>
    <Input aria-label={en ? "Qualified resource" : "资源全名"} placeholder={scope === "namespace" ? "polaris.namespace" : "polaris.namespace.table"} value={name} onChange={(event) => setName(event.target.value)} />
    {operation !== "delete" && <Input aria-label={en ? "Comment" : "备注"} placeholder={en ? "Comment" : "备注"} value={comment} onChange={(event) => setComment(event.target.value)} maxLength={1024} />}
    {operation === "create" && scope === "table" && <Input.TextArea aria-label={en ? "Fields (name: type, one per line)" : "字段（每行一个 名称: 类型）"} value={fields} rows={4} onChange={(event) => setFields(event.target.value)} />}
    {operation === "delete" ? <Popconfirm title={en ? `Remove ${name.trim()} from Polaris?` : `从 Polaris 移除 ${name.trim()}？`} onConfirm={apply}>
      <Button danger loading={busy} disabled={!name.trim()}>{en ? "Remove" : "移除"}</Button>
    </Popconfirm> : <Button type="primary" loading={busy} disabled={!name.trim()} onClick={() => void apply()}>{en ? "Apply" : "应用"}</Button>}
  </Space>;
}
