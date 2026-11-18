import {
  App, Button, Card, Empty, Input, Popconfirm, Select, Space, Table, Tabs,
  Tag, Tree, Typography,
} from "antd";
import type { DataNode } from "antd/es/tree";
import { useCallback, useEffect, useState } from "react";
import { useAuth } from "../auth";
import { useI18n } from "../i18n";
import { hasPermission } from "../permissions";
import { apiUrl } from "../runtimeConfig";
import { formatTimestamp } from "../utils";
import "./SqlPage.css";

interface Grant {
  userId: string;
  table: string;
  tableUuid: string;
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
}

interface TableInfo {
  "current-schema-id": number;
  schemas: { "schema-id": number; fields: { name: string; type: unknown; required: boolean }[] }[];
}

type GrantsInfo = { grants: Grant[]; users: { id: string; name: string }[] };
const active = (query?: Query) => query?.state === "queued" || query?.state === "running";
const jsonBody = (method: string, body: object): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(apiUrl("/v1/sql" + path), { ...options, credentials: "same-origin" });
  const data = await response.json();
  if (!response.ok) throw new Error(data.message ?? response.statusText);
  return data as T;
}

function catalogTree(tables: Grant[]): DataNode[] {
  const root: DataNode[] = [];
  for (const table of tables) {
    const parts = table.table.split(".");
    let children = root;
    for (const [index, part] of parts.entries()) {
      const key = parts.slice(0, index + 1).join(".");
      const leaf = index === parts.length - 1;
      let node = children.find((item) => item.key === key);
      if (!node) {
        node = { key, title: part, selectable: leaf, isLeaf: leaf, children: [] };
        children.push(node);
      }
      children = node.children!;
    }
  }
  return root;
}

const cell = (value: unknown) => typeof value === "string" ? value : JSON.stringify(value);

export default function SqlPage() {
  const { locale } = useI18n();
  const en = locale === "en-US";
  const { message } = App.useApp();
  const { session } = useAuth();
  const manager = hasPermission(session, "sqlManage");
  const [sql, setSql] = useState("SELECT 1 AS result");
  const [tab, setTab] = useState("result");
  const [tables, setTables] = useState<Grant[]>([]);
  const [tableName, setTableName] = useState("");
  const [schema, setSchema] = useState<TableInfo>();
  const [queryID, setQueryID] = useState("");
  const [query, setQuery] = useState<Query>();
  const [history, setHistory] = useState<Query[]>([]);
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<unknown[][]>([]);
  const [loading, setLoading] = useState(false);
  const [grants, setGrants] = useState<GrantsInfo>({ grants: [], users: [] });
  const [grantUser, setGrantUser] = useState<string>();
  const [grantTable, setGrantTable] = useState("");
  const notify = useCallback((error: unknown) => {
    void message.error(error instanceof Error ? error.message : String(error));
  }, [message]);
  const refreshTables = useCallback(() => {
    void request<Grant[]>("/tables").then(setTables).catch(notify);
  }, [notify]);
  const refreshGrants = useCallback(async () => {
    setGrants(await request<GrantsInfo>("/grants"));
    refreshTables();
  }, [refreshTables]);

  useEffect(refreshTables, [refreshTables]);
  useEffect(() => {
    if (tab === "history") void request<Query[]>("/queries").then(setHistory).catch(notify);
    if (tab === "grants" && manager) void refreshGrants().catch(notify);
  }, [tab, manager, notify, refreshGrants]);
  useEffect(() => {
    setSchema(undefined);
    if (!tableName) return;
    const controller = new AbortController();
    void request<TableInfo>("/table?" + new URLSearchParams({ name: tableName }), { signal: controller.signal })
      .then(setSchema).catch((error) => { if (!controller.signal.aborted) notify(error); });
    return () => controller.abort();
  }, [tableName, notify]);
  useEffect(() => {
    setQuery(undefined);
    setPage(1);
    setRows([]);
    if (!queryID) return;
    const controller = new AbortController();
    let timer: number;
    async function poll() {
      try {
        const next = await request<Query>("/queries/" + queryID, { signal: controller.signal });
        if (controller.signal.aborted) return;
        setQuery(next);
        if (active(next)) timer = window.setTimeout(() => void poll(), 1000);
      } catch (error) {
        if (!controller.signal.aborted) notify(error);
      }
    }
    void poll();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [queryID, notify]);
  const pages = query?.pages ?? 0;
  useEffect(() => {
    setRows([]);
    if (!pages || page > pages) return;
    const controller = new AbortController();
    void request<{ rows: unknown[][] }>(`/queries/${queryID}/pages/${page}`, { signal: controller.signal })
      .then((data) => setRows(data.rows))
      .catch((error) => { if (!controller.signal.aborted) notify(error); });
    return () => controller.abort();
  }, [pages, page, queryID, notify]);

  async function submit() {
    setLoading(true);
    try {
      const next = await request<{ id: string }>("/queries", jsonBody("POST", {
        sql, requestId: crypto.randomUUID(),
      }));
      setQueryID(next.id);
      setTab("result");
    } catch (error) {
      notify(error);
    } finally {
      setLoading(false);
    }
  }
  async function updateGrant(method: "PUT" | "DELETE", userId: string, table: string) {
    try {
      await request("/grants", jsonBody(method, { userId, table }));
      await refreshGrants();
    } catch (error) { notify(error); }
  }
  const fields = schema?.schemas.find((item) => item["schema-id"] === schema["current-schema-id"])?.fields;
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
        {pages > 0 && <Button href={apiUrl(`/v1/sql/queries/${query.id}/download`)}>{en ? "Download CSV" : "下载 CSV"}</Button>}
        {query.truncated && <Tag color="gold">{en ? "Result limit reached" : "已达结果上限"}</Tag>}
        {query.state === "succeeded" && query.rowCount > 0 && pages === 0 &&
          <Tag color="gold">{en ? "Result unavailable or expired" : "结果不可访问或已过期"}</Tag>}
        {query.state === "succeeded" && <Typography.Text type="secondary">{query.rowCount.toLocaleString(locale)} {en ? "rows stored" : "行已保存"}</Typography.Text>}
      </Space>
      {query.columns && <Table
        size="small" rowKey="id" pagination={false} scroll={{ x: "max-content" }}
        columns={query.columns.map((column, i) => ({
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
    <div className="sql-panels">
      <Card size="small" title="Polaris" extra={<Button size="small" onClick={refreshTables}>{en ? "Refresh" : "刷新"}</Button>}>
        {tables.length ? <Tree treeData={catalogTree(tables)} defaultExpandAll onSelect={(keys) => setTableName(String(keys[0] ?? ""))} /> :
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={en ? "No granted tables" : "暂无已授权的表"} />}
        {fields && <div className="sql-schema">
          <Typography.Text strong>{tableName}</Typography.Text>
          <Button block size="small" onClick={() => setSql(`SELECT * FROM ${tableName} LIMIT 100`)}>{en ? "Open sample query" : "打开示例查询"}</Button>
          {fields.map((field) => <div key={field.name}>{field.name} <Typography.Text type="secondary">{cell(field.type)}</Typography.Text></div>)}
        </div>}
      </Card>
      <div className="sql-main">
        <Card size="small" title={en ? "Spark SQL" : "Spark SQL 查询"}>
          <Space direction="vertical" className="sql-full-width">
            <Input.TextArea className="sql-editor" aria-label="SQL" rows={10} value={sql} maxLength={32768}
              onChange={(event) => setSql(event.target.value)}
              onKeyDown={(event) => {
                if ((event.ctrlKey || event.metaKey) && event.key === "Enter" && sql.trim() && !loading) {
                  event.preventDefault();
                  void submit();
                }
              }} />
            <Space>
              <Button type="primary" disabled={!sql.trim()} loading={loading} onClick={() => void submit()}>{en ? "Run query" : "运行查询"}</Button>
              {query && active(query) && <Button onClick={() => void request(`/queries/${query.id}/cancel`, jsonBody("POST", {})).catch(notify)}>{en ? "Cancel query" : "取消查询"}</Button>}
            </Space>
          </Space>
        </Card>
        <Card size="small">
          <Tabs activeKey={tab} onChange={setTab} items={[
            { key: "result", label: en ? "Results" : "查询结果", children: result },
            { key: "history", label: en ? "History" : "查询历史", children: <Table
              rowKey="id" size="small" dataSource={history} pagination={{ pageSize: 10 }} scroll={{ x: "max-content" }}
              columns={[
                { title: en ? "Submitted" : "提交时间", render: (_, item) => formatTimestamp(item.createdAt) },
                { title: tableLabel, render: (_, item) => item.tables?.join(", ") },
                { title: en ? "Status" : "状态", render: (_, item) => stateTag(item) },
                { title: en ? "Query" : "查询", render: (_, item) => <Button type="link" onClick={() => { setQueryID(item.id); setTab("result"); }}>{en ? "Open" : "打开"}</Button> },
              ]}
            /> },
            ...(manager ? [{ key: "grants", label: en ? "Table grants" : "表授权", children: <>
              <Space wrap className="sql-grant-form">
                <Select showSearch optionFilterProp="label" placeholder={en ? "Select a provisioned user" : "选择已配置 SQL 身份的用户"}
                  value={grantUser} onChange={setGrantUser} options={grants.users.map(({ id, name }) => ({ value: id, label: name }))} />
                <Input aria-label={tableLabel} placeholder="polaris.namespace.table" value={grantTable} onChange={(event) => setGrantTable(event.target.value)} />
                <Button type="primary" disabled={!grantUser || !grantTable.trim()} onClick={() => {
                  if (grantUser) void updateGrant("PUT", grantUser, grantTable.trim());
                }}>{en ? "Grant SELECT" : "授予 SELECT"}</Button>
              </Space>
              <Table rowKey={(item) => item.userId + ":" + item.table} size="small" dataSource={grants.grants} scroll={{ x: "max-content" }}
                pagination={{ pageSize: 10 }} columns={[
                  { title: en ? "User" : "用户", render: (_, item) => grants.users.find((user) => user.id === item.userId)?.name ?? item.userId },
                  { title: tableLabel, dataIndex: "table" },
                  { title: en ? "Actions" : "操作", render: (_, item) => <Popconfirm title={en ? "Revoke table access?" : "撤销该表权限？"}
                    onConfirm={() => updateGrant("DELETE", item.userId, item.table)}><Button danger type="link">{en ? "Revoke" : "撤销"}</Button></Popconfirm> },
                ]} />
            </> }] : []),
          ]} />
        </Card>
      </div>
    </div>
  </div>;
}
