import { ReloadOutlined, SearchOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Input,
  Select,
  Table,
  Tag,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Navigate } from "react-router-dom";
import { useAuth } from "../auth";
import { PageHeader } from "../components/PageHeader";
import { useI18n } from "../i18n";
import { sparkService } from "../service";
import { runtimeConfig } from "../runtimeConfig";
import type { OperationAudit } from "../types";
import { formatTimestamp } from "../utils";
import { filterAudit, operationColor } from "../audit";

export function AuditPage() {
  const { session } = useAuth();
  const { t, locale } = useI18n();
  const en = locale === "en-US";
  const [rows, setRows] = useState<OperationAudit[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [operator, setOperator] = useState<string>();
  const [operation, setOperation] = useState<string>();
  const [result, setResult] = useState<string>();
  const [page, setPage] = useState(1);
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setRows(await sparkService.getAudit());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load");
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    setPage(1);
  }, [query, operator, operation, result]);
  const filtered = useMemo(
    () => filterAudit(rows, { query, operator, operation, result }),
    [rows, query, operator, operation, result],
  );
  const options = (field: "operator" | "operation" | "result") =>
    [...new Set(rows.map((r) => r[field]))]
      .filter(Boolean)
      .sort()
      .map((value) => ({ value, label: value }));
  if (!runtimeConfig.features.audit || session?.role !== "admin")
    return <Navigate to="/overview" replace />;
  const columns: ColumnsType<OperationAudit> = [
    {
      title: t("timestamp"),
      dataIndex: "timestamp",
      width: 185,
      render: (v) => formatTimestamp(v),
    },
    {
      title: en ? "Resource" : "资源",
      dataIndex: "applicationName",
      width: 240,
      ellipsis: true,
      render: (v, row) => (
        <div title={v}>
          <b>{v}</b>
          <small className="cell-subtitle">{row.namespace}</small>
        </div>
      ),
    },
    {
      title: t("auditOperator"),
      dataIndex: "operator",
      width: 150,
      ellipsis: true,
    },
    {
      title: t("operation"),
      dataIndex: "operation",
      width: 260,
      render: (v) => (
        <Tag
          className="audit-operation-tag"
          color={operationColor(v)}
          title={v}
        >
          {v}
        </Tag>
      ),
    },
    {
      title: t("result"),
      dataIndex: "result",
      width: 120,
      render: (v) => (
        <Tag
          color={
            v === "SUCCESS" ? "success" : v === "FAILED" ? "error" : "default"
          }
        >
          {v}
        </Tag>
      ),
    },
    {
      title: t("reason"),
      dataIndex: "reason",
      width: 220,
      ellipsis: true,
      render: (v) => v || "—",
    },
    {
      title: t("message"),
      dataIndex: "message",
      width: 320,
      render: (value: string) => (
        <Typography.Paragraph
          style={{ margin: 0 }}
          ellipsis={{ rows: 3, expandable: true, tooltip: value }}
        >
          {value}
        </Typography.Paragraph>
      ),
    },
  ];
  return (
    <>
      <PageHeader
        title={t("audit")}
        subtitle={
          en
            ? "Configuration changes, workflow activity and operator actions"
            : "配置变更、工作流执行与操作历史"
        }
        extra={
          <Button icon={<ReloadOutlined />} onClick={load}>
            {t("refresh")}
          </Button>
        }
      />
      <Card className="filter-card">
        <div className="audit-filters">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={
              en ? "Search resource, reason or message" : "搜索资源、原因或消息"
            }
            aria-label={en ? "Search audit" : "搜索审计"}
          />
          <Select
            allowClear
            showSearch
            value={operator}
            onChange={setOperator}
            options={options("operator")}
            placeholder={t("auditOperator")}
            aria-label={t("auditOperator")}
          />
          <Select
            allowClear
            showSearch
            value={operation}
            onChange={setOperation}
            options={options("operation")}
            placeholder={t("operation")}
            aria-label={t("operation")}
          />
          <Select
            allowClear
            value={result}
            onChange={setResult}
            options={options("result")}
            placeholder={t("result")}
            aria-label={t("result")}
          />
          <Button
            onClick={() => {
              setQuery("");
              setOperator(undefined);
              setOperation(undefined);
              setResult(undefined);
            }}
          >
            {t("resetFilters")}
          </Button>
        </div>
      </Card>
      {error && (
        <Alert
          type="error"
          showIcon
          message={error}
          action={<Button onClick={load}>{t("retry")}</Button>}
          style={{ marginBottom: 14 }}
        />
      )}
      <Card className="table-card">
        <Table
          rowKey="id"
          loading={loading}
          dataSource={filtered}
          columns={columns}
          tableLayout="fixed"
          scroll={{ x: 1495 }}
          pagination={{
            current: page,
            onChange: setPage,
            showSizeChanger: true,
            showTotal: (total) => total + (en ? " records" : " 条记录"),
          }}
          locale={{
            emptyText: (
              <div className="audit-empty">
                <Typography.Title level={4}>
                  {en ? "No matching audit records" : "暂无匹配的审计记录"}
                </Typography.Title>
                <Typography.Text type="secondary">
                  {en
                    ? "Try adjusting your filters, or perform an administrator action."
                    : "请调整筛选条件，或执行管理员操作。"}
                </Typography.Text>
              </div>
            ),
          }}
        />
      </Card>
    </>
  );
}
