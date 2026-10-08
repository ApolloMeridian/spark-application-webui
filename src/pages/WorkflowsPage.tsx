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
import { useCallback, useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { parse, stringify } from "yaml";
import dayjs from "dayjs";
import { useAuth } from "../auth";
import { useI18n } from "../i18n";
import { PageHeader } from "../components/PageHeader";
import { WorkflowEditor, WorkflowGraph } from "../components/WorkflowEditor";
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
  const admin = session?.role === "admin";
  const { message } = App.useApp();
  const location = useLocation();
  const navigate = useNavigate();
  const ns = permittedNamespaces(
    runtimeConfig.cluster.namespaces,
    session?.namespaces,
  );
  const [projects, setProjects] = useState<Project[]>([]);
  const projectKey = `spark-console-project-${session?.id}-${session?.tenantId || "default"}`;
  const [project, setProject] = useState(
    () => localStorage.getItem(projectKey) || "",
  );
  const [flows, setFlows] = useState<Workflow[]>([]);
  const [schedules, setSchedules] = useState<WorkflowSchedule[]>([]);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [tasks, setTasks] = useState<TaskRun[]>([]);
  const [templates, setTemplates] = useState<ApplicationTemplate[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editingProject, setEditingProject] = useState<Partial<Project>>();
  const [editing, setEditing] = useState<Workflow>();
  const [schedule, setSchedule] = useState<Partial<WorkflowSchedule>>();
  const [preview, setPreview] = useState<string[]>([]);
  const [versionFlow, setVersionFlow] = useState<Workflow>();
  const [versions, setVersions] = useState<WorkflowVersion[]>([]);
  const [snapshot, setSnapshot] = useState("");
  const [runDetail, setRunDetail] = useState<WorkflowRun>();
  const [triggerFlow, setTriggerFlow] = useState<Workflow>();
  const [triggerValues, setTriggerValues] = useState<
    Record<string, Record<string, string>>
  >({});
  const [backfill, setBackfill] = useState<Workflow>();
  const [backfillCron, setBackfillCron] = useState("0 2 * * *");
  const [backfillZone, setBackfillZone] = useState(
    runtimeConfig.timeZone || "Asia/Shanghai",
  );
  const [range, setRange] = useState<[string, string]>();
  const [backfillPreview, setBackfillPreview] = useState<string[]>();
  const [filterStatus, setFilterStatus] = useState<string>();
  const [filterFlow, setFilterFlow] = useState<string>();
  const [filterTrigger, setFilterTrigger] = useState<string>();
  const [filterUser, setFilterUser] = useState("");
  const [filterRange, setFilterRange] = useState<[string, string]>();
  const [editorValid, setEditorValid] = useState(true);
  useEffect(() => {
    if (!editing) setEditorValid(true);
  }, [editing]);
  const activeTab =
    location.pathname === "/schedules"
      ? "schedules"
      : location.pathname === "/workflow-history"
        ? "runs"
        : "flows";
  const report = (e: unknown) =>
    message.error(e instanceof Error ? e.message : String(e));
  const loadProjects = useCallback(async () => {
    try {
      const items = await api.projects();
      setProjects(items);
      setProject((old) =>
        items.some((p) => p.id === old) ? old : items[0]?.id || "",
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [session?.tenantId]);
  const load = useCallback(async () => {
    if (!project) {
      setFlows([]);
      setRuns([]);
      setTasks([]);
      setSchedules([]);
      return;
    }
    setLoading(true);
    setError("");
    try {
      const [f, s, r, t] = await Promise.all([
        api.workflows(project),
        api.schedules(project),
        api.runs(project),
        api.taskRuns(project),
      ]);
      setFlows(f);
      setSchedules(s);
      setRuns(r);
      setTasks(t);
      setRunDetail((old) => (old ? r.find((x) => x.id === old.id) : undefined));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [project]);
  useEffect(() => {
    void loadProjects();
    void operationsService
      .listTemplates()
      .then(setTemplates)
      .catch(() => undefined);
  }, [loadProjects]);
  useEffect(() => {
    if (project) localStorage.setItem(projectKey, project);
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
        items={[
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
            <Tag color="blue">{session?.tenantId || "default"}</Tag>
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
                    await loadProjects();
                  } catch (e) {
                    report(e);
                  }
                }}
              >
                <Button danger size="small" icon={<DeleteOutlined />} />
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
          <Card className="table-card">
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
                              cron: "0 2 * * *",
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
        okButtonProps={{ disabled: !editorValid }}
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
            onValidityChange={setEditorValid}
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
              ? "Restore creates a new version. Deleting a historical version removes its database row; run snapshots remain unchanged."
              : "回溯会生成新版本；删除历史版本会清除数据库记录，执行快照保持不变。"
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
              render: (v: number) => `v${v}`,
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
                    onClick={() => setSnapshot(stringify(v.snapshot))}
                  >
                    {en ? "View" : "查看"}
                  </Button>
                  {admin && versionFlow && (
                    <>
                      <Popconfirm
                        title={
                          en
                            ? "Restore this snapshot as a new current version?"
                            : "回溯此快照为新的当前版本？"
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
                        <Button size="small">{en ? "Restore" : "回溯"}</Button>
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
                          disabled={v.version === versionFlow.version}
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
        title={en ? "Immutable snapshot" : "不可变快照"}
        onCancel={() => setSnapshot("")}
        footer={null}
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
        width={720}
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
          <Form.Item
            label="Cron"
            extra={
              en
                ? "Five fields: minute hour day month weekday"
                : "五段：分钟 小时 日 月 星期"
            }
          >
            <Space.Compact style={{ width: "100%" }}>
              <Input
                value={schedule?.cron}
                onChange={(e) => {
                  setSchedule({ ...schedule, cron: e.target.value });
                  setPreview([]);
                }}
              />
              <Select
                placeholder={en ? "Presets" : "常用周期"}
                style={{ width: 200 }}
                options={[
                  { value: "0 * * * *", label: en ? "Every hour" : "每小时" },
                  {
                    value: "0 2 * * *",
                    label: en ? "Daily at 02:00" : "每天 02:00",
                  },
                  {
                    value: "0 2 * * 1",
                    label: en ? "Monday at 02:00" : "周一 02:00",
                  },
                ]}
                onChange={(cron) => {
                  setSchedule({ ...schedule, cron });
                  setPreview([]);
                }}
              />
            </Space.Compact>
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
            onClick={async () => {
              try {
                setPreview(
                  await api.cronPreview(
                    schedule?.cron || "",
                    schedule?.timeZone || "UTC",
                  ),
                );
              } catch (e) {
                report(e);
              }
            }}
          >
            {en ? "Preview next 10 executions" : "预览下次 10 次执行"}
          </Button>
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
        onCancel={() => setBackfill(undefined)}
        okButtonProps={{ disabled: !backfillPreview?.length }}
        okText={
          en
            ? `Confirm ${backfillPreview?.length || 0} runs`
            : `确认 ${backfillPreview?.length || 0} 次执行`
        }
        onOk={async () => {
          if (!backfill || !range) return;
          try {
            await api.backfill(project, backfill.id, {
              cron: backfillCron,
              timeZone: backfillZone,
              from: range[0],
              to: range[1],
              confirm: true,
            });
            setBackfill(undefined);
            navigate("/workflow-history");
            await load();
          } catch (e) {
            report(e);
          }
        }}
        width={720}
      >
        <Alert
          type="warning"
          message={
            en
              ? "Backfill freezes the current workflow version and uses each planned occurrence as the date baseline. Maximum 200 runs per request."
              : "补数冻结当前工作流版本，以每次计划时间作为日期基准，每次请求最多 200 个实例。"
          }
        />
        <Form layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="Cron">
            <Input
              value={backfillCron}
              onChange={(e) => {
                setBackfillCron(e.target.value);
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
              try {
                setBackfillPreview(
                  await api.backfill(project, backfill.id, {
                    cron: backfillCron,
                    timeZone: backfillZone,
                    from: range[0],
                    to: range[1],
                    confirm: false,
                  }),
                );
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
