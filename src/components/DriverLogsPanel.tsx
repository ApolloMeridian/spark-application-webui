import { CopyOutlined, ReloadOutlined } from "@ant-design/icons";
import {
  Alert,
  App,
  Button,
  Card,
  DatePicker,
  Empty,
  Input,
  InputNumber,
  Select,
  Space,
  Spin,
  Tag,
  Typography,
} from "antd";
import type { Dayjs } from "dayjs";
import { useEffect, useRef, useState } from "react";
import { useI18n } from "../i18n";
import { sparkService } from "../service";
import type {
  DriverLogQuery,
  DriverLogWindow,
  SparkApplication,
} from "../types";
import { displayNow, displayTime } from "../utils";

export function DriverLogsPanel({ app }: { app: SparkApplication }) {
  const { locale, t } = useI18n();
  const en = locale === "en-US";
  const text = (zh: string, english: string) => (en ? english : zh);
  const { message } = App.useApp();
  const [source, setSource] = useState<"kubernetes" | "loki">("kubernetes");
  const [mode, setMode] = useState<"tail" | "range">("tail");
  const [start, setStart] = useState(1);
  const [end, setEnd] = useState(2000);
  const [limit, setLimit] = useState(2000);
  const [range, setRange] = useState<[Dayjs, Dayjs]>([
    displayTime(app.startedAt ?? app.createdAt).subtract(5, "minute"),
    app.finishedAt
      ? displayTime(app.finishedAt).add(5, "minute")
      : displayNow(),
  ]);
  const [direction, setDirection] = useState<"forward" | "backward">("forward");
  const [result, setResult] = useState<DriverLogWindow>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [level, setLevel] = useState("ALL");
  const revision = useRef(0);
  const load = async (override: Partial<DriverLogQuery> = {}) => {
    const query: DriverLogQuery = {
      source,
      mode,
      limit,
      startLine: start,
      endLine: end,
      from: range[0].toISOString(),
      to: range[1].toISOString(),
      direction,
      ...override,
    };
    if (
      query.source === "kubernetes" &&
      query.mode === "range" &&
      (!query.startLine ||
        !query.endLine ||
        query.endLine < query.startLine ||
        query.endLine - query.startLine >= 10000)
    ) {
      setError(
        text(
          "行范围必须递增，每次最多查询 10000 行。",
          "Use an increasing line range of at most 10000 lines per request.",
        ),
      );
      return;
    }
    const request = ++revision.current;
    setLoading(true);
    setError("");
    setResult(undefined);
    try {
      const next = await sparkService.getDriverLogs(
        app.namespace,
        app.name,
        query,
      );
      if (request === revision.current) setResult(next);
    } catch (e) {
      if (request === revision.current)
        setError(
          e instanceof Error ? e.message : "Failed to query Driver logs",
        );
    } finally {
      if (request === revision.current) setLoading(false);
    }
  };
  const invalidate = () => {
    revision.current++;
    setResult(undefined);
    setError("");
    setLoading(false);
  };
  useEffect(() => {
    void load();
    return () => {
      revision.current++;
    };
  }, [app.namespace, app.name]);
  const lines = (result?.lines ?? [])
    .map((line, i) => ({ line, number: (result?.startLine || 1) + i }))
    .filter(
      ({ line }) =>
        (level === "ALL" ||
          line.includes(` ${level} `) ||
          line.startsWith(`${level} `)) &&
        line.toLowerCase().includes(search.toLowerCase()),
    );
  const page = (nextStart: number) => {
    const width = end - start + 1;
    const nextEnd = nextStart + width - 1;
    setStart(nextStart);
    setEnd(nextEnd);
    void load({ startLine: nextStart, endLine: nextEnd });
  };
  return (
    <Card
      className="panel-card logs-card"
      title={
        <Space wrap>
          <span>{app.driverPod}</span>
          <Tag>Driver logs</Tag>
        </Space>
      }
    >
      <Space wrap style={{ marginBottom: 12 }}>
        <Select
          aria-label={text("日志来源", "Log source")}
          value={source}
          onChange={(value) => {
            setSource(value);
            invalidate();
          }}
          style={{ width: 210 }}
          options={[
            { value: "kubernetes", label: "Kubernetes" },
            {
              value: "loki",
              label: text("Loki 持久化日志", "Loki retained logs"),
            },
          ]}
        />
        {source === "kubernetes" ? (
          <>
            <Select
              aria-label={text("查询方式", "Query mode")}
              value={mode}
              onChange={(value) => {
                setMode(value);
                invalidate();
              }}
              style={{ width: 170 }}
              options={[
                { value: "tail", label: text("最新日志", "Latest logs") },
                { value: "range", label: text("指定行范围", "Line range") },
              ]}
            />
            {mode === "range" ? (
              <>
                <InputNumber
                  aria-label={text("起始行", "Start line")}
                  min={1}
                  precision={0}
                  value={start}
                  onChange={(v) => {
                    setStart(v ?? 1);
                    invalidate();
                  }}
                />
                <span>—</span>
                <InputNumber
                  aria-label={text("结束行", "End line")}
                  min={1}
                  precision={0}
                  value={end}
                  onChange={(v) => {
                    setEnd(v ?? 2000);
                    invalidate();
                  }}
                />
              </>
            ) : null}
          </>
        ) : (
          <>
            <DatePicker.RangePicker
              showTime
              value={range}
              onChange={(v) => {
                if (v?.[0] && v[1]) {
                  setRange([v[0], v[1]]);
                  invalidate();
                }
              }}
            />
            <Select
              aria-label={text("排序", "Order")}
              value={direction}
              onChange={(v) => {
                setDirection(v);
                invalidate();
              }}
              options={[
                { value: "forward", label: t("oldestFirst") },
                { value: "backward", label: t("newestFirst") },
              ]}
            />
          </>
        )}
        {(source === "loki" || mode === "tail") && (
          <Select
            aria-label={text("每次行数", "Line limit")}
            value={limit}
            onChange={(v) => {
              setLimit(v);
              invalidate();
            }}
            options={[500, 2000, 5000, 10000].map((value) => ({
              value,
              label: `${value} ${text("行", "lines")}`,
            }))}
          />
        )}
        <Button
          icon={<ReloadOutlined />}
          loading={loading}
          onClick={() => void load()}
        >
          {t("queryLogs")}
        </Button>
        {source === "kubernetes" && mode === "range" && (
          <>
            <Button
              disabled={
                loading || !result || result.source === "snapshot" || start <= 1
              }
              onClick={() => page(Math.max(1, start - (end - start + 1)))}
            >
              {text("上一段", "Previous range")}
            </Button>
            <Button
              disabled={
                loading || !result?.hasMore || result.source === "snapshot"
              }
              onClick={() =>
                page(
                  result?.byteLimit && result.endLine
                    ? result.endLine + 1
                    : end + 1,
                )
              }
            >
              {text("下一段", "Next range")}
            </Button>
          </>
        )}
      </Space>
      <Typography.Paragraph type="secondary">
        {source === "kubernetes"
          ? text(
              "行号相对于 Kubernetes 当前保留的容器日志；最新模式显示窗口内行号。每次最多 10000 行 / 4 MiB；早期行范围会流式跳过之前的内容。已轮转或删除的日志请查询 Loki。",
              "Line numbers refer to the container log currently retained by Kubernetes; latest mode uses window-relative numbers. Each request returns up to 10000 lines / 4 MiB, streaming past earlier lines. Use Loki for rotated or deleted logs.",
            )
          : text(
              "按时间查询已采集的 Driver 日志，需要配置 Loki 并采集 spark_role=driver；达到行数上限时请缩小时间范围。",
              "Query collected Driver logs by time. Loki must be configured and ingest spark_role=driver. Narrow the time range when the entry limit is reached.",
            )}
      </Typography.Paragraph>
      {error && (
        <Alert
          type="error"
          showIcon
          message={error}
          action={<Button onClick={() => void load()}>{t("retry")}</Button>}
          style={{ marginBottom: 12 }}
        />
      )}
      {error && app.logs.length > 0 && (
        <Button
          style={{ marginBottom: 12 }}
          onClick={() =>
            setResult({
              source: "snapshot",
              mode: "tail",
              lines: app.logs,
              startLine: 0,
              endLine: 0,
              hasMore: false,
              byteLimit: false,
              limit: 5000,
            })
          }
        >
          {text("查看已保存的诊断尾部", "Show saved diagnostic tail")}
        </Button>
      )}
      {result?.source === "snapshot" && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={text(
            "当前显示已保存的诊断快照（最多 5000 行），并非完整历史日志，也不是所选行范围。",
            "Showing the saved diagnostic snapshot (up to 5000 lines), not the full history or the selected line range.",
          )}
        />
      )}
      {result &&
        (result.byteLimit || (source === "loki" && result.hasMore)) && (
          <Alert
            type="warning"
            showIcon
            message={
              result.byteLimit
                ? text(
                    "已达到 4 MiB 响应上限，请继续查询下一段或缩小范围。",
                    "The 4 MiB response limit was reached. Query the next range or narrow the range.",
                  )
                : text(
                    `已达到 Loki 查询上限（${result.limit} 行），请缩小时间范围以查看其余内容。`,
                    `Loki query limit reached (${result.limit} lines). Narrow the time range to retrieve remaining entries.`,
                  )
            }
            style={{ marginBottom: 12 }}
          />
        )}
      <Space wrap style={{ marginBottom: 12 }}>
        <Input
          aria-label={text("筛选当前日志", "Filter current logs")}
          placeholder={text("筛选当前窗口", "Filter current window")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Select
          aria-label={text("日志级别", "Log level")}
          value={level}
          onChange={setLevel}
          options={["ALL", "INFO", "WARN", "ERROR"].map((value) => ({
            value,
            label: value,
          }))}
        />
        <Button
          disabled={!lines.length}
          icon={<CopyOutlined />}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(
                lines.map((x) => x.line).join("\n"),
              );
              void message.success(t("copied"));
            } catch {
              void message.error(text("复制失败", "Copy failed"));
            }
          }}
        >
          {t("copy")}
        </Button>
        {result && (
          <Tag>
            {result.startLine
              ? `${result.startLine}–${result.endLine}`
              : `${result.lines.length} ${text("行", "lines")}`}
          </Tag>
        )}
      </Space>
      {loading ? (
        <Spin />
      ) : (
        <pre className="log-viewer">
          {lines.length ? (
            lines.map(({ line, number }) => (
              <div
                key={number}
                className={
                  line.includes("ERROR")
                    ? "log-error"
                    : line.includes("WARN")
                      ? "log-warn"
                      : ""
                }
              >
                <span
                  style={{ minWidth: "8ch", width: "auto", paddingRight: 12 }}
                >
                  {number}
                </span>
                {line}
              </div>
            ))
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />
          )}
        </pre>
      )}
    </Card>
  );
}
