import { hasPermission } from '../permissions';
import {
  ClockCircleOutlined,
  CopyOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  HistoryOutlined,
  PlayCircleOutlined,
  PlusOutlined,
  ReloadOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import {
  Alert,
  App,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Statistic,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
  Upload,
} from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { parse, stringify } from "yaml";
import dayjs from "dayjs";
import { useAuth } from "../auth";
import { useI18n } from "../i18n";
import { PageHeader } from "../components/PageHeader";
import { WorkflowEditor, WorkflowGraph } from "../components/WorkflowEditor";
import { CronEditor } from "../components/CronEditor";
import { runtimeConfig } from "../runtimeConfig";
import { operationsService } from "../service";
import type { ApplicationTemplate } from "../types";
import { formatTimestamp, permittedNamespaces } from "../utils";
import {
  newWorkflow,
  validateDAG,
  workflowService as api,
  type Project,
  type Workflow,
  type WorkflowRun,
  type WorkflowSchedule,
  type WorkflowVersion,
  type TaskRun,
  type Tenant,
  type BackfillRequest,
} from "../workflows";

const colors: Record<string, string> = {
  queued: "default",
  running: "blue",
  paused: "orange",
  succeeded: "green",
  failed: "red",
  cancelled: "default",
  skipped: "orange",
  discarded: "default",
  cancelling: "orange",
};
const terminal = (state: string) =>
  ["succeeded", "failed", "cancelled", "discarded"].includes(state);
function Status({ value, en }: { value: string; en: boolean }) {
  const labels: Record<string, string> = {
    queued: "排队",
    running: "运行中",
    paused: "暂停",
    succeeded: "成功",
    failed: "失败",
    cancelled: "已终止",
    skipped: "跳过",
    discarded: "已抛弃",
    cancelling: "终止中",
  };
  return <Tag color={colors[value]}>{en ? value : labels[value] || value}</Tag>;
}

export function WorkflowsPage() {
  const { locale } = useI18n();
  const en = locale === "en-US";
  const { session } = useAuth();
  const admin = hasPermission(session, "workflowsWrite");
  const { message } = App.useApp();
  const location = useLocation();
  const navigate = useNavigate();
  const ns = permittedNamespaces(
    runtimeConfig.cluster.namespaces,
    session?.namespaces,
  );
  const [projects, setProjects] = useState<Project[]>([]);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [projectsReady, setProjectsReady] = useState(false);
  const loadRevision = useRef(0);
  const projectRevision = useRef(0);
  const currentProject = useRef("");
  const projectKey = `spark-console-project-${session?.id}-${session?.tenantId || "default"}`;
  const [project, setProject] = useState(
    () => localStorage.getItem(projectKey) || "",
  );
  const [flows, setFlows] = useState<Workflow[]>([]);
  currentProject.current = project;
  const [schedules, setSchedules] = useState<WorkflowSchedule[]>([]);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [tasks, setTasks] = useState<TaskRun[]>([]);
  const [backfillRequests, setBackfillRequests] = useState<BackfillRequest[]>(
    [],
  );
  const [templates, setTemplates] = useState<ApplicationTemplate[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editingProject, setEditingProject] = useState<Partial<Project>>();
  const [editing, setEditing] = useState<Workflow>();
  const [schedule, setSchedule] = useState<Partial<WorkflowSchedule>>();
  const [preview, setPreview] = useState<string[]>([]);
  const [previewError, setPreviewError] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const previewRevision = useRef(0);
  useEffect(() => {
    ++previewRevision.current;
    setPreview([]);
    setPreviewError("");
    setPreviewLoading(false);
  }, [
    schedule?.cron,
    schedule?.timeZone,
    schedule?.startsAt,
    schedule?.endsAt,
    schedule?.id,
  ]);
  const [versionFlow, setVersionFlow] = useState<Workflow>();
  const [versions, setVersions] = useState<WorkflowVersion[]>([]);
  const [snapshot, setSnapshot] = useState("");
  const [viewingVersion, setViewingVersion] = useState<WorkflowVersion>();
  const [runDetail, setRunDetail] = useState<WorkflowRun>();
  const [triggerFlow, setTriggerFlow] = useState<Workflow>();
  const [triggerValues, setTriggerValues] = useState<
    Record<string, Record<string, string>>
  >({});
  const [backfill, setBackfill] = useState<Workflow>();
  const [backfillCron, setBackfillCron] = useState("0 0 2 * * *");
  const [backfillZone, setBackfillZone] = useState(
    runtimeConfig.timeZone || "Asia/Shanghai",
  );
  const [range, setRange] = useState<[string, string]>();
  const [backfillPreview, setBackfillPreview] = useState<string[]>();
  const [backfillDirection, setBackfillDirection] = useState<
    "forward" | "backward"
  >("forward");
  const [backfillFailurePolicy, setBackfillFailurePolicy] = useState<
    "stop" | "continue"
  >("stop");
  const [backfillSubmitting, setBackfillSubmitting] = useState(false);
  const backfillRevision = useRef(0);
  useEffect(() => {
    ++backfillRevision.current;
    setBackfillPreview(undefined);
  }, [
    backfill?.id,
    backfillCron,
    backfillZone,
    range?.[0],
    range?.[1],
    backfillDirection,
    backfillFailurePolicy,
  ]);
  const [filterStatus, setFilterStatus] = useState<string>();
  const [filterFlow, setFilterFlow] = useState<string>();
  const [filterTrigger, setFilterTrigger] = useState<string>();
  const [filterUser, setFilterUser] = useState("");
  const [filterRange, setFilterRange] = useState<[string, string]>();
  const activeTab =
    location.pathname === "/schedules"
      ? "schedules"
      : location.pathname === "/workflow-history"
        ? "runs"
        : "flows";
  const report = (e: unknown) =>
    message.error(e instanceof Error ? e.message : String(e));
  const loadProjects = useCallback(async () => {
    const revision = ++projectRevision.current;
    ++loadRevision.current;
    setProjectsReady(false);
    try {
      const items = await api.projects();
      if (revision !== projectRevision.current) return;
      setProjects(items);
      setProject((old) =>
        items.some((p) => p.id === old) ? old : items[0]?.id || "",
      );
      setError("");
      setProjectsReady(true);
    } catch (e) {
      if (revision !== projectRevision.current) return;
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [session?.tenantId]);
  const load = useCallback(async () => {
    const revision = ++loadRevision.current;
    if (!projectsReady) return;
    if (!project || !projects.some((p) => p.id === project)) {
      setFlows([]);
      setRuns([]);
      setTasks([]);
      setBackfillRequests([]);
      setSchedules([]);
      setRunDetail(undefined);
      setLoading(false);
      setError("");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const [f, s, r, t, b] = await Promise.all([
        api.workflows(project),
        api.schedules(project),
        api.runs(project),
        api.taskRuns(project),
        api.backfills(project),
      ]);
      if (
        revision !== loadRevision.current ||
        currentProject.current !== project
      )
        return;
      setFlows(f);
      setSchedules(s);
      setRuns(r);
      setTasks(t);
      setBackfillRequests(b);
      setRunDetail((old) => (old ? r.find((x) => x.id === old.id) : undefined));
    } catch (e) {
      if (
        revision !== loadRevision.current ||
        currentProject.current !== project
      )
        return;
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (revision === loadRevision.current) setLoading(false);
    }
  }, [project, projectsReady, projects]);
  useEffect(() => {
    void loadProjects();
    let active = true;
    void api
      .tenants()
      .then((items) => {
        if (active) setTenants(items);
      })
      .catch(() => undefined);
    void operationsService
      .listTemplates()
      .then(setTemplates)
      .catch(() => undefined);
    return () => {
      active = false;
      ++loadRevision.current;
      ++projectRevision.current;
    };
  }, [loadProjects]);
  useEffect(() => {
    if (project) localStorage.setItem(projectKey, project);
    else localStorage.removeItem(projectKey);
  }, [project, projectKey]);
  useEffect(() => {
    void load();
    const timer = setInterval(() => {
      void load();
    }, 10000);
    return () => clearInterval(timer);
  }, [load]);
  const execute = async (operation: () => Promise<unknown>) => {
    try {
      await operation();
      await load();
    } catch (e) {
      report(e);
    }
  };
  const showVersions = async (flow: Workflow) => {
    try {
      setVersions(await api.versions(project, flow.id));
      setVersionFlow(flow);
    } catch (e) {
      report(e);
    }
  };
  const saveFlow = async () => {
    if (!editing) return;
    const validation = validateDAG(editing.nodes);
    if (validation) {
      report(validation);
      return;
    }
    setSaving(true);
    try {
      await api.saveWorkflow(editing);
      setEditing(undefined);
      await load();
      message.success(
        en ? "Workflow saved as a new version" : "工作流已保存为新版本",
      );
    } catch (e) {
      report(e);
    } finally {
      setSaving(false);
    }
  };
  const selectedProject = projects.find((p) => p.id === project);
  const filteredRuns = runs.filter(
    (r) =>
      (!filterStatus || r.status === filterStatus) &&
      (!filterFlow || r.workflowId === filterFlow) &&
      (!filterTrigger || r.trigger === filterTrigger) &&
      (!filterUser || r.createdBy.includes(filterUser)) &&
      (!filterRange ||
        (r.createdAt >= filterRange[0] && r.createdAt <= filterRange[1])),
  );
  const allowedRunIds = new Set(filteredRuns.map((r) => r.id));
  const filteredTasks = tasks.filter((t) => allowedRunIds.has(t.runId));
  const taskColumns = [
    {
      title: en ? "Task instance" : "任务实例",
      dataIndex: "name",
      width: 350,
      ellipsis: true,
    },
    {
      title: en ? "Status" : "状态",
      dataIndex: "status",
      width: 110,
      render: (v: string) => <Status value={v} en={en} />,
    },
    { title: en ? "Attempt" : "尝试", dataIndex: "attempt", width: 85 },
    {
      title: "SparkApplication",
      width: 250,
      render: (_: unknown, t: TaskRun) => (
        <Button
          type="link"
          title={t.applicationName}
          style={{ maxWidth: "100%" }}
          onClick={() =>
            navigate(
              `/applications/${encodeURIComponent(t.namespace)}/${encodeURIComponent(t.applicationName)}`,
            )
          }
        >
          <span
            style={{
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {t.applicationName}
          </span>
        </Button>
      ),
    },
    {
      title: en ? "Started" : "开始时间",
      dataIndex: "startedAt",
      width: 175,
      render: (v: string) => formatTimestamp(v),
    },
    {
      title: en ? "Finished" : "完成时间",
      dataIndex: "finishedAt",
      width: 175,
      render: (v: string) => formatTimestamp(v),
    },
    {
      title: en ? "Message" : "消息",
      dataIndex: "message",
      width: 230,
      ellipsis: true,
    },
  ];
  const histories = (
    <>
      <Space wrap className="workflow-history-filters">
        <Select
          allowClear
          placeholder={en ? "Workflow" : "工作流"}
          value={filterFlow}
          onChange={setFilterFlow}
          style={{ width: 180 }}
          options={flows.map((f) => ({ value: f.id, label: f.name }))}
        />
        <Select
          allowClear
          placeholder={en ? "Status" : "状态"}
          value={filterStatus}
          onChange={setFilterStatus}
          style={{ width: 150 }}
          options={Object.keys(colors).map((value) => ({
            value,
            label: value,
          }))}
        />
        <Select
          allowClear
          placeholder={en ? "Trigger" : "触发来源"}
          value={filterTrigger}
          onChange={setFilterTrigger}
          style={{ width: 140 }}
          options={["manual", "schedule", "backfill"].map((value) => ({
            value,
            label: value,
          }))}
        />
        <Input
          placeholder={en ? "Operator" : "操作人"}
          value={filterUser}
          onChange={(e) => setFilterUser(e.target.value)}
          style={{ width: 150 }}
        />
        <DatePicker.RangePicker
          showTime
          onChange={(values) =>
            setFilterRange(
              values?.[0] && values[1]
                ? [values[0].toISOString(), values[1].toISOString()]
                : undefined,
            )
          }
        />
        <Button
          onClick={() => {
            setFilterFlow(undefined);
            setFilterStatus(undefined);
            setFilterTrigger(undefined);
            setFilterUser("");
            setFilterRange(undefined);
          }}
        >
          {en ? "Reset" : "重置"}
        </Button>
      </Space>
      <Tabs
        defaultActiveKey="workflow"
        items={[
          {
            key: "backfills",
            label: en ? "Backfill requests" : "补数请求",
            children: (
              <>
                <Alert
                  type="info"
                  showIcon
                  message={
                    en
                      ? "The whole range is persisted and generated in batches. Cancel stops future generation only; existing instances remain in execution history."
                      : "整个范围持久化后分批生成实例。取消只停止后续生成，已生成的实例仍保留在执行历史中。"
                  }
                />
                <Table
                  rowKey="id"
                  dataSource={backfillRequests}
                  loading={loading}
                  scroll={{ x: 1100 }}
                  pagination={{ pageSize: 10 }}
                  columns={[
                    {
                      title: en ? "Workflow" : "工作流",
                      dataIndex: "workflowName",
                      width: 200,
                    },
                    {
                      title: en ? "Version" : "版本",
                      dataIndex: "version",
                      render: (v) => `v${v}`,
                      width: 80,
                    },
                    {
                      title: en ? "Status" : "状态",
                      dataIndex: "status",
                      render: (v) => (
                        <Tag>
                          {en
                            ? v
                            : (
                                {
                                  queued: "排队",
                                  completed: "生成完成",
                                  blocked: "已阻断",
                                  failed: "已失败",
                                  cancelled: "已取消",
                                } as Record<string, string>
                              )[v] || v}
                        </Tag>
                      ),
                      width: 120,
                    },
                    {
                      title: en ? "Created instances" : "已生成实例",
                      dataIndex: "createdCount",
                      width: 120,
                    },
                    { title: "Cron", dataIndex: "cron", width: 150 },
                    {
                      title: en ? "Order" : "顺序",
                      dataIndex: "direction",
                      width: 100,
                      render: (v) =>
                        en ? v : v === "backward" ? "倒序" : "正序",
                    },
                    {
                      title: en ? "On failure" : "失败策略",
                      dataIndex: "failurePolicy",
                      width: 140,
                      render: (v) =>
                        en ? v : v === "continue" ? "继续补数" : "停止补数",
                    },
                    {
                      title: en ? "Next logical time" : "下个基准时间",
                      render: (_, b) =>
                        b.status === "queued" ? formatTimestamp(b.nextAt) : "—",
                      width: 180,
                    },
                    {
                      title: en ? "End" : "结束",
                      dataIndex: "endsAt",
                      render: (v) => formatTimestamp(v),
                      width: 180,
                    },
                    {
                      title: en ? "Error" : "错误",
                      dataIndex: "error",
                      width: 220,
                    },
                    {
                      title: en ? "Actions" : "操作",
                      width: 120,
                      fixed: "right",
                      render: (_, b) =>
                        admin && (
                          <Popconfirm
                            title={
                              en
                                ? "Stop generating remaining instances?"
                                : "停止生成剩余实例？"
                            }
                            onConfirm={() =>
                              execute(() => api.cancelBackfill(project, b.id))
                            }
                          >
                            <Button
                              danger
                              size="small"
                              disabled={
                                !["queued", "blocked"].includes(b.status)
                              }
                            >
                              {en ? "Cancel" : "取消"}
                            </Button>
                          </Popconfirm>
                        ),
                    },
                  ]}
                />
              </>
            ),
          },
          {
            key: "workflow",
            label: en ? "Workflow execution history" : "工作流执行历史",
            children: (
              <Table
                rowKey="id"
                dataSource={filteredRuns}
                loading={loading}
                scroll={{ x: 1250 }}
                pagination={{ pageSize: 15 }}
                columns={[
                  {
                    title: en ? "Workflow instance" : "工作流实例",
                    dataIndex: "name",
                    width: 310,
                    ellipsis: true,
                    render: (v: string, r: WorkflowRun) => (
                      <Button type="link" onClick={() => setRunDetail(r)}>
                        {v}
                      </Button>
                    ),
                  },
                  {
                    title: en ? "Status" : "状态",
                    dataIndex: "status",
                    width: 110,
                    render: (v: string) => <Status value={v} en={en} />,
                  },
                  {
                    title: en ? "Version" : "版本",
                    dataIndex: "version",
                    width: 80,
                    render: (v: number) => `v${v}`,
                  },
                  {
                    title: en ? "Trigger" : "触发来源",
                    dataIndex: "trigger",
                    width: 100,
                  },
                  {
                    title: en ? "Logical time" : "基准时间",
                    dataIndex: "scheduledAt",
                    width: 175,
                    render: (v: string) => formatTimestamp(v),
                  },
                  {
                    title: en ? "Created" : "创建时间",
                    dataIndex: "createdAt",
                    width: 175,
                    render: (v: string) => formatTimestamp(v),
                  },
                  {
                    title: en ? "Operator" : "操作人",
                    dataIndex: "createdBy",
                    width: 120,
                  },
                  {
                    title: en ? "Actions" : "操作",
                    fixed: "right",
                    width: 210,
                    render: (_: unknown, r: WorkflowRun) =>
                      admin && (
                        <Space wrap size={4}>
                          {!terminal(r.status) && r.status !== "cancelling" && (
                            <>
                              <Button
                                size="small"
                                onClick={() =>
                                  execute(() =>
                                    api.runAction(
                                      project,
                                      r.id,
                                      r.status === "paused"
                                        ? "resume"
                                        : "pause",
                                    ),
                                  )
                                }
                              >
                                {r.status === "paused"
                                  ? en
                                    ? "Resume"
                                    : "恢复"
                                  : en
                                    ? "Pause"
                                    : "暂停"}
                              </Button>
                              <Popconfirm
                                title={
                                  en
                                    ? "Cancel this execution and stop its tasks?"
                                    : "终止此实例及其运行中的任务？"
                                }
                                onConfirm={() =>
                                  execute(() =>
                                    api.runAction(project, r.id, "cancel"),
                                  )
                                }
                              >
                                <Button size="small" danger>
                                  {en ? "Cancel" : "终止"}
                                </Button>
                              </Popconfirm>
                            </>
                          )}
                          {r.status === "failed" && (
                            <Button
                              size="small"
                              onClick={() =>
                                execute(() =>
                                  api.runAction(project, r.id, "rerun_failed"),
                                )
                              }
                            >
                              {en ? "Rerun failed" : "重跑失败节点"}
                            </Button>
                          )}
                          {terminal(r.status) && (
                            <Popconfirm
                              title={
                                en
                                  ? "Delete execution history? SparkApplications are retained."
                                  : "删除执行历史？SparkApplication 将保留。"
                              }
                              onConfirm={() =>
                                execute(() =>
                                  api.runAction(project, r.id, "delete"),
                                )
                              }
                            >
                              <Button
                                size="small"
                                danger
                                icon={<DeleteOutlined />}
                              />
                            </Popconfirm>
                          )}
                        </Space>
                      ),
                  },
                ]}
              />
            ),
          },
          {
            key: "tasks",
            label: en ? "Task execution history" : "任务执行历史",
            children: (
              <Table
                rowKey="id"
                dataSource={filteredTasks}
                loading={loading}
                columns={taskColumns}
                scroll={{ x: 1300 }}
                pagination={{ pageSize: 20 }}
              />
            ),
          },
        ]}
      />
    </>
  );
  return (
    <>
      <PageHeader
        title={en ? "Workflow orchestration" : "工作流编排"}
        subtitle={
          en
            ? "Tenant-isolated projects · immutable versions · PostgreSQL-backed execution and scheduling"
            : "租户隔离项目 · 不可变版本 · PostgreSQL 持久化执行与调度"
        }
        extra={
          <Space>
            <Tag color="blue">
              {tenants.find((t) => t.id === (session?.tenantId || "default"))
                ?.name ||
                (session?.tenantId === "default" || !session?.tenantId
                  ? "default"
                  : en
                    ? "Current tenant"
                    : "当前租户")}
            </Tag>
            <Button
              icon={<ReloadOutlined />}
              onClick={() => {
                void loadProjects();
                void load();
              }}
            >
              {en ? "Refresh" : "刷新"}
            </Button>
            {admin && (
              <Button
                icon={<PlusOutlined />}
                onClick={() => setEditingProject({ name: "", description: "" })}
              >
                {en ? "New project" : "新建项目"}
              </Button>
            )}
          </Space>
        }
      />
      {error && (
        <Alert
          type="error"
          showIcon
          message={error}
          action={
            <Button
              onClick={() => {
                void loadProjects();
                void load();
              }}
            >
              {en ? "Retry" : "重试"}
            </Button>
          }
          style={{ marginBottom: 16 }}
        />
      )}
      <Card size="small" className="workflow-project-bar">
        <Space wrap>
          <Typography.Text strong>{en ? "Project" : "项目"}</Typography.Text>
          <Select
            style={{ width: 250 }}
            value={project || undefined}
            placeholder={en ? "Select project" : "选择项目"}
            options={projects.map((p) => ({ value: p.id, label: p.name }))}
            onChange={(value) => {
              setProject(value);
              setRunDetail(undefined);
              setFilterFlow(undefined);
            }}
          />
          {selectedProject && (
            <Typography.Text type="secondary">
              {selectedProject.description}
            </Typography.Text>
          )}
          {admin && selectedProject && (
            <>
              <Button
                size="small"
                icon={<EditOutlined />}
                onClick={() => setEditingProject(selectedProject)}
              />
              <Popconfirm
                title={en ? "Delete empty project?" : "删除空项目？"}
                onConfirm={async () => {
                  try {
                    await api.deleteProject(project);
                    ++loadRevision.current;
                    setProject("");
                    setError("");
                    await loadProjects();
                  } catch (e) {
                    report(e);
                  }
                }}
              >
                <Button
                  danger
                  size="small"
                  icon={<DeleteOutlined />}
                  aria-label={en ? "Delete project" : "删除项目"}
                />
              </Popconfirm>
            </>
          )}
        </Space>
      </Card>
      {!project ? (
        <Card>
          <Empty
            description={
              en ? "Create or select a project to begin" : "请创建或选择项目"
            }
          />
        </Card>
      ) : (
        <>
          <div className="workflow-stats">
            <Card size="small">
              <Statistic
                title={en ? "Workflows" : "工作流"}
                value={flows.length}
              />
            </Card>
            <Card size="small">
              <Statistic
                title={en ? "Schedules" : "定时配置"}
                value={schedules.filter((s) => s.enabled).length}
              />
            </Card>
            <Card size="small">
              <Statistic
                title={en ? "Active executions" : "活跃执行"}
                value={runs.filter((r) => !terminal(r.status)).length}
              />
            </Card>
            <Card size="small">
              <Statistic
                title={en ? "Successful executions" : "成功执行"}
                value={runs.filter((r) => r.status === "succeeded").length}
              />
            </Card>
          </div>
          <Card className="table-card workflow-tabs-card">
            <Tabs
              activeKey={activeTab}
              onChange={(tab) =>
                navigate(
                  tab === "flows"
                    ? "/projects"
                    : tab === "schedules"
                      ? "/schedules"
                      : "/workflow-history",
                )
              }
              items={[
                {
                  key: "flows",
                  label: en ? "Workflows & tasks" : "工作流与任务",
                  children: (
                    <>
                      <Space wrap style={{ marginBottom: 16 }}>
                        {admin && (
                          <>
                            <Button
                              type="primary"
                              icon={<PlusOutlined />}
                              onClick={() =>
                                setEditing(newWorkflow(project, ns[0] || ""))
                              }
                            >
                              {en ? "New workflow" : "新建工作流"}
                            </Button>
                            <Upload
                              accept=".yaml,.yml,.json"
                              showUploadList={false}
                              beforeUpload={async (file) => {
                                try {
                                  const imported = parse(
                                    await file.text(),
                                  ) as Workflow;
                                  setEditing({
                                    ...newWorkflow(project, ns[0] || ""),
                                    ...imported,
                                    id: "",
                                    projectId: project,
                                    version: 0,
                                  });
                                } catch (e) {
                                  report(e);
                                }
                                return false;
                              }}
                            >
                              <Button icon={<UploadOutlined />}>
                                {en ? "Import YAML" : "导入 YAML"}
                              </Button>
                            </Upload>
                          </>
                        )}
                      </Space>
                      <Table
                        rowKey="id"
                        loading={loading}
                        dataSource={flows}
                        scroll={{ x: 1100 }}
                        columns={[
                          {
                            title: en ? "Workflow" : "工作流",
                            dataIndex: "name",
                            width: 230,
                            render: (v: string, f: Workflow) => (
                              <Space>
                                <Typography.Text strong>{v}</Typography.Text>
                                <Tag color="blue">v{f.version}</Tag>
                              </Space>
                            ),
                          },
                          {
                            title: en ? "Tasks" : "任务数",
                            render: (_: unknown, f: Workflow) => f.nodes.length,
                            width: 75,
                          },
                          {
                            title: en ? "Strategy" : "策略",
                            dataIndex: "strategy",
                            width: 125,
                          },
                          {
                            title: en ? "Enabled" : "启用",
                            dataIndex: "enabled",
                            width: 85,
                            render: (v: boolean) => (
                              <Tag color={v ? "green" : "default"}>
                                {v
                                  ? en
                                    ? "Enabled"
                                    : "启用"
                                  : en
                                    ? "Disabled"
                                    : "禁用"}
                              </Tag>
                            ),
                          },
                          {
                            title: en ? "Updated" : "更新时间",
                            dataIndex: "updatedAt",
                            width: 170,
                            render: (v: string) => formatTimestamp(v),
                          },
                          {
                            title: en ? "Actions" : "操作",
                            fixed: "right",
                            width: 365,
                            render: (_: unknown, f: Workflow) => (
                              <Space size={4} wrap>
                                <Button
                                  size="small"
                                  icon={<HistoryOutlined />}
                                  onClick={() => showVersions(f)}
                                >
                                  {en ? "Versions" : "版本"}
                                </Button>
                                <Button
                                  size="small"
                                  icon={<DownloadOutlined />}
                                  aria-label={
                                    en ? "Export workflow" : "导出工作流"
                                  }
                                  onClick={() => {
                                    const blob = new Blob([stringify(f)], {
                                      type: "application/yaml",
                                    });
                                    const url = URL.createObjectURL(blob);
                                    const a = document.createElement("a");
                                    a.href = url;
                                    a.download = `${f.id}.yaml`;
                                    a.click();
                                    URL.revokeObjectURL(url);
                                  }}
                                />
                                {admin && (
                                  <>
                                    <Button
                                      size="small"
                                      type="primary"
                                      disabled={!f.enabled}
                                      icon={<PlayCircleOutlined />}
                                      onClick={() => {
                                        setTriggerFlow(f);
                                        setTriggerValues({});
                                      }}
                                    >
                                      {en ? "Run" : "执行"}
                                    </Button>
                                    <Button
                                      size="small"
                                      icon={<EditOutlined />}
                                      aria-label={
                                        en ? `Edit ${f.name}` : `编辑 ${f.name}`
                                      }
                                      onClick={() =>
                                        setEditing(structuredClone(f))
                                      }
                                    />
                                    <Button
                                      size="small"
                                      icon={<CopyOutlined />}
                                      onClick={() =>
                                        setEditing({
                                          ...structuredClone(f),
                                          id: "",
                                          name: `${f.name} copy`,
                                        })
                                      }
                                    />
                                    <Button
                                      size="small"
                                      onClick={() => {
                                        setBackfill(f);
                                        setBackfillPreview(undefined);
                                        setRange(undefined);
                                      }}
                                    >
                                      {en ? "Backfill" : "补数"}
                                    </Button>
                                    <Popconfirm
                                      title={
                                        en
                                          ? "Delete workflow and schedules? Execution history must be deleted first."
                                          : "删除工作流与定时配置？需先清除执行历史。"
                                      }
                                      onConfirm={() =>
                                        execute(() =>
                                          api.deleteWorkflow(project, f.id),
                                        )
                                      }
                                    >
                                      <Button
                                        size="small"
                                        danger
                                        icon={<DeleteOutlined />}
                                      />
                                    </Popconfirm>
                                  </>
                                )}
                              </Space>
                            ),
                          },
                        ]}
                      />
                    </>
                  ),
                },
                {
                  key: "schedules",
                  label: en ? "Schedules" : "定时调度",
                  children: (
                    <>
                      {admin && (
                        <Button
                          type="primary"
                          disabled={!flows.length}
                          icon={<ClockCircleOutlined />}
                          onClick={() => {
                            setSchedule({
                              projectId: project,
                              workflowId: flows[0]?.id,
                              name: "",
                              cron: "0 0 2 * * *",
                              timeZone:
                                runtimeConfig.timeZone ||
                                Intl.DateTimeFormat().resolvedOptions()
                                  .timeZone,
                              enabled: true,
                              misfire: "skip",
                            });
                            setPreview([]);
                          }}
                          style={{ marginBottom: 16 }}
                        >
                          {en ? "New schedule" : "新建定时配置"}
                        </Button>
                      )}
                      <Table
                        rowKey="id"
                        loading={loading}
                        dataSource={schedules}
                        scroll={{ x: 1000 }}
                        columns={[
                          {
                            title: en ? "Schedule" : "定时配置",
                            dataIndex: "name",
                          },
                          {
                            title: en ? "Workflow" : "工作流",
                            render: (_: unknown, s: WorkflowSchedule) =>
                              flows.find((f) => f.id === s.workflowId)?.name,
                          },
                          { title: "Cron", dataIndex: "cron" },
                          {
                            title: en ? "Time zone" : "时区",
                            dataIndex: "timeZone",
                          },
                          {
                            title: en ? "Misfire" : "错过策略",
                            dataIndex: "misfire",
                          },
                          {
                            title: en ? "Next execution" : "下次执行",
                            dataIndex: "nextRunAt",
                            render: (v: string, s: WorkflowSchedule) =>
                              s.enabled ? formatTimestamp(v) : "—",
                          },
                          {
                            title: en ? "Enabled" : "启用",
                            dataIndex: "enabled",
                            render: (v: boolean) => (
                              <Tag color={v ? "green" : "default"}>
                                {String(v)}
                              </Tag>
                            ),
                          },
                          {
                            title: en ? "Actions" : "操作",
                            width: 110,
                            render: (_: unknown, s: WorkflowSchedule) =>
                              admin && (
                                <Space>
                                  <Button
                                    size="small"
                                    icon={<EditOutlined />}
                                    onClick={() => {
                                      setSchedule({ ...s });
                                      setPreview([]);
                                    }}
                                  />
                                  <Popconfirm
                                    title={
                                      en ? "Delete schedule?" : "删除定时配置？"
                                    }
                                    onConfirm={() =>
                                      execute(() =>
                                        api.deleteSchedule(project, s.id),
                                      )
                                    }
                                  >
                                    <Button
                                      size="small"
                                      danger
                                      icon={<DeleteOutlined />}
                                    />
                                  </Popconfirm>
                                </Space>
                              ),
                          },
                        ]}
                      />
                    </>
                  ),
                },
                {
                  key: "runs",
                  label: en ? "Execution history" : "执行历史",
                  children: histories,
                },
              ]}
            />
          </Card>
        </>
      )}
      <Modal
        open={Boolean(editingProject)}
        title={en ? "Project" : "项目"}
        onCancel={() => setEditingProject(undefined)}
        onOk={async () => {
          if (!editingProject) return;
          try {
            const saved = await api.saveProject(editingProject);
            setEditingProject(undefined);
            await loadProjects();
            setProject(saved.id);
          } catch (e) {
            report(e);
          }
        }}
      >
        <Form layout="vertical">
          <Form.Item label={en ? "Name" : "名称"} required>
            <Input
              maxLength={100}
              value={editingProject?.name}
              onChange={(e) =>
                setEditingProject({ ...editingProject, name: e.target.value })
              }
            />
          </Form.Item>
          <Form.Item label={en ? "Description" : "描述"}>
            <Input
              value={editingProject?.description}
              onChange={(e) =>
                setEditingProject({
                  ...editingProject,
                  description: e.target.value,
                })
              }
            />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        open={Boolean(editing)}
        title={
          editing?.id
            ? en
              ? "Edit workflow · saves a new version"
              : "编辑工作流 · 保存为新版本"
            : en
              ? "New workflow"
              : "新建工作流"
        }
        width="min(1220px, 96vw)"
        styles={{
          body: { maxHeight: "calc(100vh - 240px)", overflowY: "auto" },
        }}
        onCancel={() => setEditing(undefined)}
        onOk={saveFlow}
        confirmLoading={saving}
        okText={en ? "Save version" : "保存版本"}
        destroyOnHidden
      >
        {editing && (
          <WorkflowEditor
            value={editing}
            onChange={setEditing}
            templates={templates}
            namespaces={ns}
            en={en}
          />
        )}
      </Modal>
      <Drawer
        open={Boolean(versionFlow)}
        onClose={() => setVersionFlow(undefined)}
        title={`${versionFlow?.name || ""} · ${en ? "Version history" : "版本历史"}`}
        width="min(850px, 94vw)"
      >
        <Alert
          type="info"
          message={
            en
              ? "Restore switches the current version without creating a snapshot. New edits use the next highest version number. Run snapshots remain unchanged."
              : "回溯仅切换当前版本，不创建新快照；再次编辑按最大版本号顺延。执行快照保持不变。"
          }
        />
        <Table
          rowKey="version"
          dataSource={versions}
          pagination={false}
          columns={[
            {
              title: en ? "Version" : "版本",
              dataIndex: "version",
              render: (v: number) => (
                <Space>
                  v{v}
                  {v === versionFlow?.version && (
                    <Tag color="blue">{en ? "Current" : "当前版本"}</Tag>
                  )}
                </Space>
              ),
            },
            { title: en ? "Saved by" : "保存人", dataIndex: "createdBy" },
            {
              title: en ? "Saved at" : "保存时间",
              dataIndex: "createdAt",
              render: (v: string) => formatTimestamp(v),
            },
            {
              title: en ? "Actions" : "操作",
              render: (_: unknown, v: WorkflowVersion) => (
                <Space>
                  <Button
                    size="small"
                    onClick={() => {
                      setViewingVersion(v);
                      setSnapshot(stringify(v.snapshot));
                    }}
                  >
                    {en ? "View" : "查看"}
                  </Button>
                  {admin && versionFlow && (
                    <>
                      <Popconfirm
                        title={
                          en
                            ? "Switch the current version to this snapshot?"
                            : "将当前版本切换到此快照？"
                        }
                        onConfirm={async () => {
                          try {
                            const saved = await api.versionAction(
                              project,
                              versionFlow.id,
                              v.version,
                              false,
                            );
                            await showVersions(saved);
                            await load();
                          } catch (e) {
                            report(e);
                          }
                        }}
                      >
                        <Button
                          size="small"
                          disabled={v.version === versionFlow.version}
                        >
                          {en ? "Restore" : "回溯"}
                        </Button>
                      </Popconfirm>
                      <Popconfirm
                        title={
                          en
                            ? "Permanently delete this version?"
                            : "永久删除此历史版本？"
                        }
                        onConfirm={async () => {
                          try {
                            await api.versionAction(
                              project,
                              versionFlow.id,
                              v.version,
                              true,
                            );
                            await showVersions(versionFlow);
                          } catch (e) {
                            report(e);
                          }
                        }}
                      >
                        <Button
                          size="small"
                          danger
                          disabled={
                            v.version === versionFlow.version ||
                            v.version === viewingVersion?.version
                          }
                          aria-label={`${en ? "Delete version" : "删除版本"} ${v.version}`}
                          icon={<DeleteOutlined />}
                        />
                      </Popconfirm>
                    </>
                  )}
                </Space>
              ),
            },
          ]}
        />
      </Drawer>
      <Modal
        open={Boolean(snapshot)}
        title={
          viewingVersion
            ? `${versionFlow?.name || ""} · ${en ? "Viewing version" : "查看版本"} v${viewingVersion.version}${viewingVersion.version === versionFlow?.version ? (en ? " (current)" : "（当前版本）") : ""}`
            : en
              ? "Immutable snapshot"
              : "不可变快照"
        }
        onCancel={() => {
          setSnapshot("");
          setViewingVersion(undefined);
        }}
        footer={
          viewingVersion && admin ? (
            <Space>
              <Button danger disabled>
                {en ? "Delete this version" : "删除此版本"}
              </Button>
              <Button
                type="primary"
                onClick={() => {
                  setEditing(structuredClone(viewingVersion.snapshot));
                  setSnapshot("");
                  setViewingVersion(undefined);
                  setVersionFlow(undefined);
                }}
              >
                {en ? "Edit as a new version" : "编辑并保存为新版本"}
              </Button>
            </Space>
          ) : null
        }
        width="min(1000px, 94vw)"
      >
        <Input.TextArea
          className="yaml-editor"
          readOnly
          value={snapshot}
          autoSize={{ minRows: 15, maxRows: 30 }}
        />
      </Modal>
      <Modal
        open={Boolean(triggerFlow)}
        title={en ? "Execute workflow" : "执行工作流"}
        onCancel={() => setTriggerFlow(undefined)}
        onOk={async () => {
          if (!triggerFlow) return;
          try {
            await api.trigger(project, triggerFlow.id, triggerValues);
            setTriggerFlow(undefined);
            navigate("/workflow-history");
            await load();
          } catch (e) {
            report(e);
          }
        }}
        okText={en ? "Run now" : "立即执行"}
      >
        <Alert
          type="info"
          message={
            en
              ? "Manual execution uses the current time as its date-parameter baseline. Each task is dry-run validated before dispatch."
              : "手动执行以当前时间为日期参数基准，任务派发前进行 Dry Run 校验。"
          }
        />
        <Button
          style={{ margin: "12px 0" }}
          onClick={async () => {
            if (!triggerFlow) return;
            try {
              const results = await api.dryRun(project, triggerFlow.id);
              setSnapshot(results.map((p) => p.serverYaml).join("\n---\n"));
            } catch (e) {
              report(e);
            }
          }}
        >
          {en ? "Dry run & preview" : "Dry Run 并预览"}
        </Button>
        {triggerFlow?.nodes.map((n) => (
          <Card
            size="small"
            key={n.id}
            title={n.name}
            style={{ marginBottom: 10 }}
          >
            {n.parameters.length ? (
              n.parameters.map((p) => (
                <Form.Item label={p.name} key={p.name}>
                  <Input
                    value={
                      triggerValues[n.id]?.[p.name] ??
                      n.values[p.name] ??
                      p.default ??
                      ""
                    }
                    onChange={(e) =>
                      setTriggerValues({
                        ...triggerValues,
                        [n.id]: {
                          ...triggerValues[n.id],
                          [p.name]: e.target.value,
                        },
                      })
                    }
                  />
                </Form.Item>
              ))
            ) : (
              <Typography.Text type="secondary">
                {en ? "No runtime parameters" : "无需运行参数"}
              </Typography.Text>
            )}
          </Card>
        ))}
      </Modal>
      <Modal
        open={Boolean(schedule)}
        title={en ? "Schedule configuration" : "定时配置"}
        onCancel={() => setSchedule(undefined)}
        onOk={async () => {
          if (!schedule) return;
          try {
            await api.saveSchedule(schedule);
            setSchedule(undefined);
            await load();
          } catch (e) {
            report(e);
          }
        }}
        width="min(720px, 94vw)"
        styles={{
          body: { maxHeight: "calc(100vh - 240px)", overflowY: "auto" },
        }}
      >
        <Form layout="vertical">
          <Space wrap>
            <Form.Item label={en ? "Name" : "名称"} required>
              <Input
                value={schedule?.name}
                onChange={(e) =>
                  setSchedule({ ...schedule, name: e.target.value })
                }
              />
            </Form.Item>
            <Form.Item label={en ? "Workflow" : "工作流"} required>
              <Select
                style={{ width: 240 }}
                value={schedule?.workflowId}
                options={flows.map((f) => ({ value: f.id, label: f.name }))}
                onChange={(workflowId) =>
                  setSchedule({ ...schedule, workflowId })
                }
              />
            </Form.Item>
            <Form.Item label={en ? "Enabled" : "启用"}>
              <Switch
                checked={schedule?.enabled}
                onChange={(enabled) => setSchedule({ ...schedule, enabled })}
              />
            </Form.Item>
          </Space>
          <Form.Item label="Cron">
            <CronEditor
              en={en}
              value={schedule?.cron ?? ""}
              onChange={(cron) => {
                setSchedule({ ...schedule, cron });
                setPreview([]);
              }}
            />
          </Form.Item>
          <Space wrap>
            <Form.Item label={en ? "IANA time zone" : "IANA 时区"}>
              <Input
                value={schedule?.timeZone}
                onChange={(e) => {
                  setSchedule({ ...schedule, timeZone: e.target.value });
                  setPreview([]);
                }}
              />
            </Form.Item>
            <Form.Item label={en ? "Missed execution policy" : "错过执行策略"}>
              <Select
                style={{ width: 220 }}
                value={schedule?.misfire}
                options={[
                  {
                    value: "skip",
                    label: en ? "Skip missed runs" : "跳过错过的执行",
                  },
                  {
                    value: "fire_once",
                    label: en ? "Fire latest once" : "仅补最近一次",
                  },
                  {
                    value: "catch_up",
                    label: en ? "Catch up (max 20)" : "补跑（最多 20 次）",
                  },
                ]}
                onChange={(misfire) => setSchedule({ ...schedule, misfire })}
              />
            </Form.Item>
          </Space>
          <Form.Item
            label={en ? "Optional validity window" : "有效时间范围（可选）"}
          >
            <DatePicker.RangePicker
              showTime
              value={
                schedule?.startsAt && schedule.endsAt
                  ? [dayjs(schedule.startsAt), dayjs(schedule.endsAt)]
                  : undefined
              }
              onChange={(v) =>
                setSchedule({
                  ...schedule,
                  startsAt: v?.[0]?.toISOString() || "",
                  endsAt: v?.[1]?.toISOString() || "",
                })
              }
            />
          </Form.Item>
          <Button
            loading={previewLoading}
            onClick={async () => {
              const revision = ++previewRevision.current;
              setPreview([]);
              setPreviewError("");
              setPreviewLoading(true);
              try {
                const times = await api.cronPreview(
                  schedule?.cron || "",
                  schedule?.timeZone || "UTC",
                  { startsAt: schedule?.startsAt, endsAt: schedule?.endsAt },
                );
                if (revision === previewRevision.current) setPreview(times);
              } catch (e) {
                if (revision === previewRevision.current)
                  setPreviewError(
                    (en
                      ? "Time range / Cron configuration is incorrect, or the preview could not be loaded: "
                      : "时间范围 / Cron 配置不正确，或预览请求失败：") +
                      (e instanceof Error ? e.message : String(e)),
                  );
              } finally {
                if (revision === previewRevision.current)
                  setPreviewLoading(false);
              }
            }}
          >
            {en ? "Preview next 10 executions" : "预览下次 10 次执行"}
          </Button>
          {previewError && (
            <Alert
              showIcon
              type="error"
              message={previewError}
              style={{ marginTop: 12 }}
            />
          )}
          {preview.length > 0 && (
            <div className="workflow-cron-preview">
              {preview.map((at) => (
                <Tag key={at}>{formatTimestamp(at)}</Tag>
              ))}
            </div>
          )}
        </Form>
      </Modal>
      <Modal
        open={Boolean(backfill)}
        title={en ? "Backfill logical schedule times" : "按逻辑调度时间补数"}
        onCancel={() => !backfillSubmitting && setBackfill(undefined)}
        confirmLoading={backfillSubmitting}
        okButtonProps={{ disabled: !backfillPreview?.length }}
        okText={en ? "Confirm entire range" : "确认整个范围"}
        onOk={async () => {
          if (!backfill || !range || backfillSubmitting) return;
          setBackfillSubmitting(true);
          try {
            await api.backfill(project, backfill.id, {
              cron: backfillCron,
              timeZone: backfillZone,
              from: range[0],
              to: range[1],
              confirm: true,
              direction: backfillDirection,
              failurePolicy: backfillFailurePolicy,
            });
            setBackfill(undefined);
            message.success(
              en
                ? "Backfill request saved; instances will be generated in batches."
                : "补数请求已保存，实例将分批生成。",
            );
            navigate("/workflow-history");
            await load();
          } catch (e) {
            report(e);
          } finally {
            setBackfillSubmitting(false);
          }
        }}
        width="min(720px, 94vw)"
        styles={{
          body: { maxHeight: "calc(100vh - 240px)", overflowY: "auto" },
        }}
      >
        <Alert
          type="warning"
          message={
            en
              ? "Preview shows only the first 50 occurrences; confirmation saves the entire range without an instance cap. Backfill instances run serially, below normal schedules. A running instance is not interrupted; queued schedules run next. Large or second-level ranges may produce many jobs."
              : "预览仅显示前 50 次，确认后保存整个范围，不限制总实例数。补数串行执行，优先级低于正常定时任务；正在执行的实例不会被中断，结束后正常调度优先。大范围或秒级 Cron 可能生成大量作业。"
          }
        />
        <Form layout="vertical" style={{ marginTop: 16 }}>
          <Space wrap>
            <Form.Item label={en ? "Backfill order" : "补数顺序"}>
              <Select
                style={{ width: 220 }}
                value={backfillDirection}
                options={[
                  {
                    value: "forward",
                    label: en ? "Forward · oldest first" : "正序 · 从早到晚",
                  },
                  {
                    value: "backward",
                    label: en ? "Reverse · newest first" : "倒序 · 从晚到早",
                  },
                ]}
                onChange={(v) => {
                  setBackfillDirection(v);
                  setBackfillPreview(undefined);
                }}
              />
            </Form.Item>
            <Form.Item label={en ? "On instance failure" : "实例失败后"}>
              <Select
                style={{ width: 220 }}
                value={backfillFailurePolicy}
                options={[
                  {
                    value: "stop",
                    label: en ? "Stop remaining backfill" : "停止剩余补数",
                  },
                  {
                    value: "continue",
                    label: en
                      ? "Continue other occurrences"
                      : "继续其他时间补数",
                  },
                ]}
                onChange={(v) => {
                  setBackfillFailurePolicy(v);
                  setBackfillPreview(undefined);
                }}
              />
            </Form.Item>
          </Space>
          <Form.Item label="Cron">
            <CronEditor
              en={en}
              value={backfillCron}
              onChange={(cron) => {
                setBackfillCron(cron);
                setBackfillPreview(undefined);
              }}
            />
          </Form.Item>
          <Form.Item label={en ? "Time zone" : "时区"}>
            <Input
              value={backfillZone}
              onChange={(e) => {
                setBackfillZone(e.target.value);
                setBackfillPreview(undefined);
              }}
            />
          </Form.Item>
          <Form.Item label={en ? "Range" : "范围"}>
            <DatePicker.RangePicker
              showTime
              value={range ? [dayjs(range[0]), dayjs(range[1])] : null}
              onChange={(v) => {
                setRange(
                  v?.[0] && v[1]
                    ? [v[0].toISOString(), v[1].toISOString()]
                    : undefined,
                );
                setBackfillPreview(undefined);
              }}
            />
          </Form.Item>
          <Button
            disabled={!range}
            onClick={async () => {
              if (!backfill || !range) return;
              const revision = backfillRevision.current;
              try {
                const result = await api.backfill(project, backfill.id, {
                  cron: backfillCron,
                  timeZone: backfillZone,
                  from: range[0],
                  to: range[1],
                  confirm: false,
                  direction: backfillDirection,
                  failurePolicy: backfillFailurePolicy,
                });
                if (revision === backfillRevision.current)
                  setBackfillPreview(result);
              } catch (e) {
                report(e);
              }
            }}
          >
            {en ? "Preview occurrences" : "预览执行时间"}
          </Button>
          {backfillPreview && (
            <div className="workflow-cron-preview">
              {backfillPreview.length ? (
                backfillPreview.map((at) => (
                  <Tag key={at}>{formatTimestamp(at)}</Tag>
                ))
              ) : (
                <Empty />
              )}
            </div>
          )}
        </Form>
      </Modal>
      <Drawer
        open={Boolean(runDetail)}
        width="min(1250px, 96vw)"
        title={runDetail?.name}
        onClose={() => setRunDetail(undefined)}
      >
        {runDetail && (
          <>
            <Descriptions
              column={3}
              items={[
                {
                  key: "state",
                  label: en ? "Status" : "状态",
                  children: <Status value={runDetail.status} en={en} />,
                },
                {
                  key: "version",
                  label: en ? "Frozen version" : "冻结版本",
                  children: `v${runDetail.version}`,
                },
                {
                  key: "time",
                  label: en ? "Logical time" : "基准时间",
                  children: formatTimestamp(runDetail.scheduledAt),
                },
                {
                  key: "start",
                  label: en ? "Started" : "开始",
                  children: formatTimestamp(runDetail.startedAt),
                },
                {
                  key: "end",
                  label: en ? "Finished" : "完成",
                  children: formatTimestamp(runDetail.finishedAt),
                },
                {
                  key: "user",
                  label: en ? "Operator" : "操作人",
                  children: runDetail.createdBy,
                },
              ]}
            />
            {runDetail.message && (
              <Alert type="warning" message={runDetail.message} />
            )}
            <Card size="small" style={{ margin: "16px 0" }}>
              <WorkflowGraph
                nodes={runDetail.snapshot.nodes}
                en={en}
                onSelect={(id) => {
                  const task = runDetail.tasks.find((t) => t.nodeId === id);
                  if (task) setSnapshot(task.manifest);
                }}
              />
            </Card>
            <Table
              rowKey="id"
              dataSource={runDetail.tasks}
              columns={taskColumns}
              scroll={{ x: 1300 }}
              pagination={false}
            />
          </>
        )}
      </Drawer>
    </>
  );
}
