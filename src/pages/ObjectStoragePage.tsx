import {
  CopyOutlined,
  DeleteOutlined,
  DownloadOutlined,
  FileTextOutlined,
  FileOutlined,
  FolderOutlined,
  ReloadOutlined,
  FileAddOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import {
  Alert,
  App,
  Breadcrumb,
  Button,
  Card,
  Checkbox,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Progress,
  Select,
  Space,
  Spin,
  Table,
  Tabs,
  Tag,
  Typography,
} from "antd";
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { useAuth } from "../auth";
import { useI18n } from "../i18n";
import { PageHeader } from "../components/PageHeader";
import { formatTimestamp } from "../utils";
import {
  storageActions,
  storageAllowed,
  storageService,
  TEXT_LIMIT,
  formatStorageSize,
  isStorageText,
  textExtensions,
  type PolicyInfo,
  type StorageAction,
  type StorageConnection,
  type StorageInfo,
  type StorageObject,
  type StorageOperation,
  type StorageTarget,
} from "../objectStorage";
const StorageTextEditor = lazy(() => import("../components/StorageTextEditor"));

const errors: Record<string, [string, string]> = {
  text_too_large: [
    "文件超过编辑大小上限，不能保存",
    "File exceeds the editable size limit; cannot save",
  ],
  not_text: [
    "不支持此文件类型或编码，请下载查看",
    "Unsupported file type or encoding; download instead",
  ],
  version_conflict: [
    "对象已经变化或目标已存在，请刷新后重试",
    "Object changed or destination exists; refresh before retrying",
  ],
  access_denied: [
    "没有访问此目录或执行此操作的权限",
    "No permission for this directory or action",
  ],
  storage_unavailable: [
    "对象存储服务不可用，请检查插件并重试",
    "Object storage unavailable; check the plugin and retry",
  ],
  upload_busy: ["上传正在处理中，请稍后重试", "Upload is busy; retry shortly"],
  upload_not_pending: [
    "上传已结束，请刷新确认对象状态",
    "Upload has ended; refresh to check the object",
  ],
  invalid_ticket: [
    "上传凭证已失效，请重新上传",
    "Upload ticket expired or invalid; restart the upload",
  ],
  conditional_writes_not_supported: [
    "此连接未启用安全条件写，不能上传或编辑",
    "Safe conditional writes are disabled for this connection",
  ],
  mock_upload_limit: [
    "Mock 仅支持不超过 1 MiB 的文本上传；大文件请用本机插件测试",
    "Mock accepts text uploads up to 1 MiB; use the local plugin for larger files",
  ],
  operation_audit_pending: [
    "操作可能已生效，审计结果待确认，请刷新检查，不要重复覆盖",
    "Operation may have applied; audit pending. Refresh instead of overwriting again",
  ],
};
export default function ObjectStoragePage() {
  const { session } = useAuth();
  const { locale } = useI18n();
  const en = locale === "en-US";
  const { message } = App.useApp();
  const service = storageService(session);
  const text = (zh: string, english: string) => (en ? english : zh);
  const [info, setInfo] = useState<StorageInfo>();
  const [selected, setSelected] = useState("");
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const previewRevision = useRef(0);
  const [prefix, setPrefix] = useState("");
  const [objects, setObjects] = useState<StorageObject[]>([]);
  const [next, setNext] = useState("");
  const [cursors, setCursors] = useState<string[]>([""]);
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [tab, setTab] = useState("files");
  const [preview, setPreview] = useState<{
    object: StorageObject;
    content: string;
    truncated?: boolean;
    readOnly?: boolean;
    creating?: boolean;
  }>();
  const [saving, setSaving] = useState(false);
  const [copy, setCopy] = useState<StorageObject>();
  const [copyKey, setCopyKey] = useState("");
  const [progress, setProgress] = useState<number>();
  const [file, setFile] = useState<File>();
  const [uploadOpen, setUploadOpen] = useState(false);
  const [resume, setResume] = useState(false);
  const uploadController = useRef<AbortController>();
  const requestRevision = useRef(0);
  const listRevision = useRef(0);
  const [policies, setPolicies] = useState<PolicyInfo>({
    grants: [],
    users: [],
  });
  const [grantOpen, setGrantOpen] = useState(false);
  const [grantForm] = Form.useForm();
  const [operations, setOperations] = useState<StorageOperation[]>([]);
  const root = selected
    ? (JSON.parse(selected) as {
        connection: string;
        bucket: string;
        prefix: string;
      })
    : undefined;
  const connection = info?.connections.find((c) => c.id === root?.connection);
  const textLimit = info?.textMaxBytes ?? TEXT_LIMIT;
  const target = (key = prefix): StorageTarget => ({
    connection: root?.connection || "",
    bucket: root?.bucket || "",
    key,
  });
  const allowed = (a: StorageAction, key = prefix) =>
    storageAllowed(session, connection, root?.bucket || "", key, a);
  const report = (e: unknown) => {
    const code = e instanceof Error ? e.message : "storage_unavailable";
    const value = errors[code];
    return value
      ? value[en ? 1 : 0]
      : text("请求失败：", "Request failed: ") + code;
  };
  const load = useCallback(async () => {
    const rev = ++requestRevision.current;
    setBusy(true);
    setError("");
    try {
      const x = await service.info();
      if (rev !== requestRevision.current) return;
      setInfo(x);
      setSelected((old) => {
        if (old) {
          const r = JSON.parse(old) as {
            connection: string;
            bucket: string;
            prefix: string;
          };
          if (
            x.connections.some(
              (c) =>
                c.id === r.connection &&
                c.roots.some(
                  (g) => g.bucket === r.bucket && g.prefix === r.prefix,
                ),
            )
          )
            return old;
        }
        const c = x.connections.find((c) =>
          c.roots.some((g) => g.actions.includes("list")),
        );
        const g = c?.roots.find((g) => g.actions.includes("list"));
        return c && g
          ? JSON.stringify({
              connection: c.id,
              bucket: g.bucket,
              prefix: g.prefix,
            })
          : "";
      });
    } catch (e) {
      if (rev === requestRevision.current)
        setError(e instanceof Error ? e.message : "storage_unavailable");
    } finally {
      if (rev === requestRevision.current) setBusy(false);
    }
  }, [service]);
  useEffect(() => {
    void load();
    return () => {
      requestRevision.current++;
      listRevision.current++;
    };
  }, [load, session?.id, session?.role]);
  useEffect(() => {
    if (selected) {
      const r = JSON.parse(selected) as { prefix: string };
      setPrefix(r.prefix);
    } else {
      setPrefix("");
      setObjects([]);
    }
    setPage(0);
    setCursors([""]);
    setPreview(undefined);
    setCopy(undefined);
    previewRevision.current++;
  }, [selected]);
  const refreshList = useCallback(async () => {
    if (!selected) return;
    const r = JSON.parse(selected) as {
      connection: string;
      bucket: string;
      prefix: string;
    };
    if (!prefix.startsWith(r.prefix)) return;
    const rev = ++listRevision.current;
    setBusy(true);
    setError("");
    try {
      const out = await service.list(
        { connection: r.connection, bucket: r.bucket, key: prefix },
        cursors[page] || "",
      );
      if (rev === listRevision.current) {
        setObjects(out.objects);
        setNext(out.next);
      }
    } catch (e) {
      if (rev === listRevision.current) {
        setObjects([]);
        setError(e instanceof Error ? e.message : "storage_unavailable");
      }
    } finally {
      if (rev === listRevision.current) setBusy(false);
    }
  }, [service, selected, prefix, page, cursors]);
  useEffect(() => {
    void refreshList();
    return () => {
      listRevision.current++;
    };
  }, [refreshList]);
  const navigate = (value: string) => {
    setPrefix(value);
    setPage(0);
    setCursors([""]);
    setPreview(undefined);
    previewRevision.current++;
  };
  const showText = async (o: StorageObject) => {
    const revision = ++previewRevision.current;
    const origin = selected;
    try {
      const out = await service.text(target(o.key));
      if (
        origin === selectedRef.current &&
        revision === previewRevision.current
      )
        setPreview(out);
    } catch (e) {
      void message.error(report(e));
    }
  };
  const mutate = async (fn: () => Promise<unknown>) => {
    setSaving(true);
    try {
      await fn();
      void message.success(text("操作成功", "Operation completed"));
      await refreshList();
    } catch (e) {
      void message.error(report(e));
    } finally {
      setSaving(false);
    }
  };
  const upload = async () => {
    if (!file || !root) return;
    if (file.size > (info?.maxUploadBytes || 0)) {
      void message.error(
        text("超过上传大小上限", "Upload size limit exceeded"),
      );
      return;
    }
    const controller = new AbortController();
    uploadController.current = controller;
    setProgress(0);
    try {
      await service.upload(
        target(prefix + file.name),
        file,
        setProgress,
        controller.signal,
        resume,
      );
      setUploadOpen(false);
      setFile(undefined);
      void message.success(text("上传完成", "Upload completed"));
      await refreshList();
    } catch (e) {
      void message.error(
        controller.signal.aborted
          ? text("已请求取消上传", "Upload cancellation requested")
          : report(e),
      );
    } finally {
      setProgress(undefined);
      uploadController.current = undefined;
    }
  };
  const loadPolicies = async () => {
    try {
      setPolicies(await service.policies());
    } catch (e) {
      void message.error(report(e));
    }
  };
  useEffect(() => {
    if (tab === "permissions") void loadPolicies();
    if (tab === "audit")
      void service
        .operations()
        .then(setOperations)
        .catch((e) => message.error(report(e)));
  }, [tab, session?.id]);
  const roots =
    info?.connections.flatMap((c) =>
      c.roots
        .filter((g) => g.actions.includes("list"))
        .map((g) => ({
          value: JSON.stringify({
            connection: c.id,
            bucket: g.bucket,
            prefix: g.prefix,
          }),
          label: `${c.name} · ${g.bucket}/${g.prefix}`,
        })),
    ) || [];
  const crumbs = [
    { title: root?.bucket || "", onClick: () => root && navigate(root.prefix) },
    ...(root
      ? prefix
          .slice(root.prefix.length)
          .split("/")
          .filter(Boolean)
          .map((segment, index, segments) => ({
            title: segment,
            onClick: () =>
              navigate(
                root.prefix + segments.slice(0, index + 1).join("/") + "/",
              ),
          }))
      : []),
  ];
  const connections = info?.connections || [];
  return (
    <div className="object-storage-page">
      <PageHeader
        title={text("对象存储", "Object storage")}
        subtitle={text(
          "按目录授权的文件访问 · 独立无状态插件",
          "Prefix-authorized file access · independent stateless plugin",
        )}
        extra={
          <Button
            icon={<ReloadOutlined />}
            onClick={async () => {
              await load();
              if (tab === "permissions") await loadPolicies();
            }}
          >
            {text("刷新连接", "Refresh connections")}
          </Button>
        }
      />
      {error && (
        <Alert
          type="error"
          showIcon
          message={report(new Error(error))}
          action={
            <Button onClick={() => void load().then(refreshList)}>
              {text("重试", "Retry")}
            </Button>
          }
          style={{ marginBottom: 16 }}
        />
      )}
      <Card>
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            {
              key: "files",
              label: text("文件浏览", "Files"),
              children: (
                <>
                  <Space wrap style={{ marginBottom: 16, width: "100%" }}>
                    <Select
                      aria-label={text("存储目录", "Storage root")}
                      value={selected || undefined}
                      placeholder={text(
                        "选择已授权目录",
                        "Select an authorized root",
                      )}
                      options={roots}
                      disabled={saving || uploadOpen}
                      onChange={setSelected}
                      style={{ width: 440, maxWidth: "75vw" }}
                    />
                    <Tag color="blue">
                      {connection?.provider.toUpperCase() || "S3 / GCS"}
                    </Tag>
                    <Button
                      icon={<ReloadOutlined />}
                      disabled={!root}
                      onClick={() => void refreshList()}
                    />
                    <Button
                      type="primary"
                      icon={<UploadOutlined />}
                      aria-label={text("上传文件", "Upload file")}
                      disabled={!allowed("upload") || !connection?.editable}
                      onClick={() => {
                        setUploadOpen(true);
                        setResume(false);
                      }}
                    >
                      {text("上传文件", "Upload file")}
                    </Button>
                    <Button
                      icon={<FileAddOutlined />}
                      aria-label={text("新建文本文件", "New text file")}
                      disabled={
                        !allowed("upload") || !connection?.editable || saving
                      }
                      onClick={() => {
                        previewRevision.current++;
                        setPreview({
                          creating: true,
                          content: "",
                          object: {
                            key: prefix + "untitled.txt",
                            size: 0,
                            modified: "",
                            directory: false,
                            version: "",
                          },
                        });
                      }}
                    >
                      {text("新建文本文件", "New text file")}
                    </Button>
                  </Space>
                  {root && (
                    <Breadcrumb
                      items={crumbs}
                      style={{ marginBottom: 16, overflowWrap: "anywhere" }}
                    />
                  )}
                  {!root && !busy ? (
                    <Empty
                      description={text(
                        "暂无可浏览目录，请联系管理员配置授权",
                        "No browsable directories; ask an administrator for access",
                      )}
                    />
                  ) : (
                    <Table
                      size="small"
                      rowKey="key"
                      loading={busy}
                      dataSource={objects}
                      pagination={false}
                      scroll={{ x: 900 }}
                      columns={[
                        {
                          title: text("名称", "Name"),
                          dataIndex: "key",
                          width: 390,
                          render: (_: string, o: StorageObject) => (
                            <Space>
                              <span>
                                {o.directory ? (
                                  <FolderOutlined
                                    style={{ color: "#d8a534" }}
                                  />
                                ) : isStorageText(o.key) ? (
                                  <FileTextOutlined />
                                ) : (
                                  <FileOutlined style={{ color: "#8492a6" }} />
                                )}
                              </span>
                              <Button
                                type="link"
                                style={{
                                  maxWidth: 330,
                                  whiteSpace: "normal",
                                  overflowWrap: "anywhere",
                                  textAlign: "left",
                                  height: "auto",
                                }}
                                disabled={
                                  !o.directory && !allowed("read", o.key)
                                }
                                onClick={() =>
                                  o.directory
                                    ? navigate(o.key)
                                    : void showText(o)
                                }
                              >
                                {o.key.slice(prefix.length) || o.key}
                              </Button>
                            </Space>
                          ),
                        },
                        {
                          title: text("大小", "Size"),
                          width: 110,
                          render: (_: unknown, o: StorageObject) =>
                            o.directory ? "—" : formatStorageSize(o.size),
                        },
                        {
                          title: text("修改时间", "Modified"),
                          width: 200,
                          render: (_: unknown, o: StorageObject) =>
                            o.directory ? "—" : formatTimestamp(o.modified),
                        },
                        {
                          title: text("操作", "Actions"),
                          width: 220,
                          render: (_: unknown, o: StorageObject) =>
                            !o.directory && (
                              <Space>
                                <Button
                                  aria-label={text("下载", "Download")}
                                  icon={<DownloadOutlined />}
                                  disabled={
                                    !allowed("download", o.key) ||
                                    o.size > (info?.maxDownloadBytes || 0)
                                  }
                                  onClick={() => {
                                    try {
                                      service.download(target(o.key));
                                    } catch (e) {
                                      void message.error(report(e));
                                    }
                                  }}
                                />
                                <Button
                                  aria-label={text("复制", "Copy")}
                                  icon={<CopyOutlined />}
                                  disabled={
                                    !allowed("copy-source", o.key) ||
                                    !connection?.editable
                                  }
                                  onClick={() => {
                                    setCopy(o);
                                    setCopyKey(o.key + ".copy");
                                  }}
                                />
                                <Popconfirm
                                  title={text(
                                    "删除这个对象？此操作不可撤销",
                                    "Delete this object? This cannot be undone",
                                  )}
                                  onConfirm={() =>
                                    mutate(() =>
                                      service.delete(target(o.key), o.version),
                                    )
                                  }
                                >
                                  <Button
                                    aria-label={text("删除", "Delete")}
                                    danger
                                    icon={<DeleteOutlined />}
                                    disabled={!allowed("delete", o.key)}
                                  />
                                </Popconfirm>
                              </Space>
                            ),
                        },
                      ]}
                    />
                  )}
                  <Space style={{ marginTop: 16 }}>
                    <Button
                      disabled={page === 0 || busy}
                      onClick={() => setPage(page - 1)}
                    >
                      {text("上一页", "Previous")}
                    </Button>
                    <span>
                      {text("第", "Page ")} {page + 1} {text("页", "")}
                    </span>
                    <Button
                      disabled={!next || busy}
                      onClick={() => {
                        setCursors([...cursors.slice(0, page + 1), next]);
                        setPage(page + 1);
                      }}
                    >
                      {text("下一页", "Next")}
                    </Button>
                  </Space>
                </>
              ),
            },
            ...(session?.role === "admin"
              ? [
                  {
                    key: "permissions",
                    label: text("目录权限", "Prefix permissions"),
                    children: (
                      <>
                        <Alert
                          type="info"
                          showIcon
                          message={text(
                            "管理员可跨租户配置用户的存储桶、目录前缀与操作权限，但授权范围不得超出管理员自身权限。授权归属于目标用户的实际租户；用户访问同时受该租户的对象存储权限约束。Helm 初始授权仅在首次匹配用户时应用。",
                            "Administrators may manage bucket, prefix and operation permissions across tenants, within their own access scope. Grants belong to the target user’s actual tenant, and access also requires that tenant’s object storage permission. Helm bootstrap grants are applied once when the matching account becomes available.",
                          )}
                          style={{ marginBottom: 16 }}
                        />
                        <Button
                          type="primary"
                          onClick={() => {
                            void loadPolicies();
                            grantForm.resetFields();
                            grantForm.setFieldsValue({
                              connection: connection?.id,
                              bucket: root?.bucket,
                              prefix: root?.prefix,
                              actions: ["list", "read", "download"],
                            });
                            setGrantOpen(true);
                          }}
                        >
                          {text("添加或更新授权", "Add or update grant")}
                        </Button>
                        <Table
                          style={{ marginTop: 16 }}
                          rowKey="id"
                          size="small"
                          dataSource={policies.grants}
                          scroll={{ x: 1080 }}
                          columns={[
                            {
                              title: text("用户", "User"),
                              render: (_: unknown, g) =>
                                policies.users.find((u) => u.id === g.userId)
                                  ?.username || g.userId,
                              width: 160,
                            },
                            {
                              title: text("租户", "Tenant"),
                              render: (_: unknown, g) => {
                                const u = policies.users.find(
                                  (u) =>
                                    u.id === g.userId &&
                                    u.tenantId === g.tenantId,
                                );
                                return u?.tenantName || g.tenantId;
                              },
                              width: 130,
                            },
                            {
                              title: text(
                                "连接 / Bucket",
                                "Connection / bucket",
                              ),
                              render: (_: unknown, g) =>
                                `${g.connection} / ${g.bucket}`,
                              width: 180,
                            },
                            {
                              title: "Prefix",
                              dataIndex: "prefix",
                              render: (v: string) =>
                                v || text("整个 Bucket", "Entire bucket"),
                              width: 230,
                            },
                            {
                              title: text("权限", "Permissions"),
                              dataIndex: "actions",
                              render: (v: string[]) =>
                                v.map((a) => <Tag key={a}>{a}</Tag>),
                            },
                            {
                              title: text("操作", "Actions"),
                              width: 90,
                              render: (_: unknown, g) => (
                                <Popconfirm
                                  title={text(
                                    "撤销该授权？",
                                    "Revoke this grant?",
                                  )}
                                  onConfirm={async () => {
                                    await mutate(() =>
                                      service.deleteGrant(g.id),
                                    );
                                    await loadPolicies();
                                    await load();
                                  }}
                                >
                                  <Button danger icon={<DeleteOutlined />} />
                                </Popconfirm>
                              ),
                            },
                          ]}
                        />
                      </>
                    ),
                  },
                  {
                    key: "audit",
                    label: text("存储操作记录", "Storage operations"),
                    children: (
                      <>
                        <Typography.Paragraph type="secondary">
                          {text(
                            "记录上传、下载、新建、编辑、删除、复制和权限变更；不记录目录浏览或分片。",
                            "Records uploads, downloads, creates, edits, deletes, copies and policy changes; not browsing or individual parts.",
                          )}
                        </Typography.Paragraph>
                        <Button
                          onClick={() =>
                            void service.operations().then(setOperations)
                          }
                        >
                          {text("刷新", "Refresh")}
                        </Button>
                        <Table
                          rowKey="id"
                          size="small"
                          dataSource={operations}
                          scroll={{ x: 900 }}
                          columns={[
                            {
                              title: text("时间", "Time"),
                              dataIndex: "created",
                              width: 200,
                              render: (v: string) => formatTimestamp(v),
                            },
                            {
                              title: text("操作人", "User"),
                              dataIndex: "user",
                              width: 140,
                            },
                            {
                              title: text("操作", "Action"),
                              dataIndex: "action",
                              width: 140,
                            },
                            {
                              title: text("对象", "Object"),
                              dataIndex: "key",
                              ellipsis: true,
                            },
                            {
                              title: text("结果", "Result"),
                              dataIndex: "result",
                              width: 120,
                              render: (v: string) => (
                                <Tag
                                  color={
                                    v === "succeeded"
                                      ? "green"
                                      : v === "pending"
                                        ? "blue"
                                        : "orange"
                                  }
                                >
                                  {v}
                                </Tag>
                              ),
                            },
                          ]}
                        />
                      </>
                    ),
                  },
                ]
              : []),
          ]}
        />
      </Card>
      <Drawer
        title={`${preview?.creating ? text("新建文本文件", "New text file") : text("文本预览", "Text preview")} · ${preview?.object.key || ""}`}
        width="min(1100px, 95vw)"
        destroyOnClose
        open={!!preview}
        onClose={() => {
          previewRevision.current++;
          setPreview(undefined);
        }}
        extra={
          <Button
            type="primary"
            loading={saving}
            disabled={
              !preview ||
              preview.readOnly ||
              preview.truncated ||
              !allowed(
                preview.creating ? "upload" : "edit",
                preview.object.key,
              ) ||
              !isStorageText(preview.object.key) ||
              !connection?.editable ||
              new TextEncoder().encode(preview?.content || "").length >
                textLimit
            }
            onClick={() =>
              preview &&
              void mutate(async () => {
                const out = preview.creating
                  ? await service.create(
                      target(preview.object.key),
                      preview.content,
                    )
                  : await service.save(
                      target(preview.object.key),
                      preview.content,
                      preview.object.version,
                    );
                setPreview({ ...preview, object: out, creating: false });
              })
            }
          >
            {text("保存", "Save")}
          </Button>
        }
      >
        {preview?.creating && (
          <div style={{ marginBottom: 16 }}>
            <Input
              aria-label={text("文件路径", "File path")}
              value={preview.object.key}
              disabled={saving}
              onChange={(e) =>
                setPreview({
                  ...preview,
                  object: { ...preview.object, key: e.target.value },
                })
              }
            />
            <Typography.Text type="secondary">
              {text("支持格式：", "Supported formats: ")}
              {textExtensions.join(", ")}
            </Typography.Text>
          </div>
        )}
        {preview?.truncated && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message={text(
              `文件大小 ${formatStorageSize(preview.object.size)}，仅显示前 ${formatStorageSize(textLimit)}，只读。超出部分请下载到本地查看。`,
              `File size ${formatStorageSize(preview.object.size)}. Only the first ${formatStorageSize(textLimit)} is shown, read-only. Download to view the rest.`,
            )}
            action={
              <Button
                disabled={
                  !allowed("download", preview.object.key) ||
                  preview.object.size > (info?.maxDownloadBytes || 0)
                }
                onClick={() => service.download(target(preview.object.key))}
              >
                {text("下载", "Download")}
              </Button>
            }
          />
        )}
        {preview && (
          <Suspense fallback={<Spin />}>
            <StorageTextEditor
              filename={preview.object.key}
              value={preview.content}
              en={en}
              truncated={preview.truncated}
              readOnly={
                saving ||
                !!preview.readOnly ||
                !!preview.truncated ||
                !allowed(
                  preview.creating ? "upload" : "edit",
                  preview.object.key,
                ) ||
                !connection?.editable
              }
              onChange={(content) =>
                setPreview((old) => (old ? { ...old, content } : old))
              }
            />
          </Suspense>
        )}
        <Typography.Text type="secondary">
          {formatStorageSize(
            new TextEncoder().encode(preview?.content || "").length,
          )}
        </Typography.Text>
      </Drawer>
      <Modal
        title={text(
          "复制对象（目标不能已存在）",
          "Copy object (destination must not exist)",
        )}
        open={!!copy}
        onCancel={() => setCopy(undefined)}
        confirmLoading={saving}
        onOk={() =>
          copy &&
          void mutate(async () => {
            await service.copy(
              target(copy.key),
              root!.bucket,
              copyKey,
              copy.version,
            );
            setCopy(undefined);
          })
        }
        okButtonProps={{
          disabled:
            !allowed("copy-destination", copyKey) || copyKey === copy?.key,
        }}
      >
        <Input
          aria-label={text("目标对象键", "Destination key")}
          value={copyKey}
          onChange={(e) => setCopyKey(e.target.value)}
        />
      </Modal>
      <Modal
        title={text("上传文件", "Upload file")}
        open={uploadOpen}
        onCancel={() => {
          if (progress !== undefined) uploadController.current?.abort();
          else setUploadOpen(false);
        }}
        onOk={() => void upload()}
        confirmLoading={progress !== undefined}
        okButtonProps={{ disabled: !file || progress !== undefined }}
        cancelText={
          progress !== undefined
            ? text("取消上传", "Cancel upload")
            : text("关闭", "Close")
        }
      >
        <Typography.Paragraph>
          {root?.bucket}/{prefix}
        </Typography.Paragraph>
        <input
          aria-label={text("选择上传文件", "Choose upload file")}
          type="file"
          disabled={progress !== undefined}
          onChange={(e) => setFile(e.target.files?.[0])}
        />
        <div style={{ marginTop: 16 }}>
          <Checkbox
            checked={resume}
            disabled={progress !== undefined}
            onChange={(e) => setResume(e.target.checked)}
          >
            {text(
              "恢复上次未完成上传（选择相同文件）",
              "Resume unfinished upload (select the same file)",
            )}
          </Checkbox>
        </div>
        <Typography.Paragraph type="secondary">
          {text(
            "默认不覆盖已有对象。分片上传可在插件重建后恢复。",
            "Existing objects are not overwritten. Multipart uploads can resume after plugin replacement.",
          )}
        </Typography.Paragraph>
        {progress !== undefined && <Progress percent={Math.round(progress)} />}
      </Modal>
      <Modal
        title={text("目录权限", "Prefix permissions")}
        open={grantOpen}
        onCancel={() => setGrantOpen(false)}
        confirmLoading={saving}
        onOk={() =>
          void grantForm
            .validateFields()
            .then((values) =>
              mutate(async () => {
                await service.saveGrant(
                  {
                    userId: values.userId,
                    connection: values.connection,
                    bucket: values.bucket,
                    prefix: values.prefix || "",
                    actions: values.actions,
                  },
                  !!values.confirmRoot,
                );
                setGrantOpen(false);
                await loadPolicies();
                await load();
              }),
            )
            .catch(() => {})
        }
      >
        <Form form={grantForm} layout="vertical">
          <Form.Item
            name="userId"
            label={text("用户", "User")}
            rules={[{ required: true }]}
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={policies.users.map((u) => ({
                value: u.id,
                label: `${u.username} (${u.tenantName || u.tenantId || u.role})${u.storageEnabled === false ? text(" · 租户未启用对象存储", " · Storage disabled for tenant") : ""}`,
                disabled: u.storageEnabled === false,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="connection"
            label={text("连接", "Connection")}
            rules={[{ required: true }]}
          >
            <Select
              options={connections.map((c: StorageConnection) => ({
                value: c.id,
                label: c.name,
              }))}
            />
          </Form.Item>
          <Form.Item name="bucket" label="Bucket" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item
            name="prefix"
            label="Prefix"
            help={text(
              "目录前缀须以 / 结尾，例如 team/analytics/",
              "Directory prefixes must end in /, e.g. team/analytics/",
            )}
          >
            <Input />
          </Form.Item>
          <Form.Item
            name="actions"
            label={text("操作权限", "Actions")}
            rules={[{ required: true }]}
          >
            <Checkbox.Group options={[...storageActions]} />
          </Form.Item>
          <Form.Item name="confirmRoot" valuePropName="checked">
            <Checkbox>
              {text(
                "如果 Prefix 为空，确认授权整个 Bucket",
                "If Prefix is empty, confirm access to the entire bucket",
              )}
            </Checkbox>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
