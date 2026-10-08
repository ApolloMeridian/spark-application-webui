import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import {
  App,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";
import { useAuth } from "../auth";
import { PageHeader } from "../components/PageHeader";
import { useI18n } from "../i18n";
import type { CreateUserInput, UpdateUserInput, UserAccount } from "../types";
import { userService } from "../userService";
import { formatTimestamp } from "../utils";
import { runtimeConfig } from "../runtimeConfig";
import { workflowService, type Tenant } from "../workflows";

type UserForm = CreateUserInput & { disabled: boolean };

export function UsersPage() {
  const { session, updateSession } = useAuth();
  const { t, locale } = useI18n();
  const en = locale === "en-US";
  const { message } = App.useApp();
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [tenantsOpen, setTenantsOpen] = useState(false);
  const [editingTenant, setEditingTenant] = useState<Partial<Tenant>>();
  const loadTenants = async () => {
    try {
      setTenants(await workflowService.tenants());
    } catch (error) {
      message.error(String(error));
    }
  };
  const [rows, setRows] = useState<UserAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<UserAccount | null>(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm<UserForm>();
  const load = async () => {
    setLoading(true);
    try {
      setRows(await userService.list());
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void load();
    void loadTenants();
  }, []);
  const showCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({
      role: "viewer",
      tenantId: "default",
      namespaces: [],
      disabled: false,
    });
    setOpen(true);
  };
  const showEdit = (user: UserAccount) => {
    setEditing(user);
    form.setFieldsValue({
      username: user.username,
      displayName: user.displayName,
      email: user.email,
      role: user.role === "admin" ? "admin" : "viewer",
      tenantId: user.tenantId || "default",
      namespaces: user.namespaces ?? [],
      disabled: user.disabled,
      password: "",
    });
    setOpen(true);
  };
  const save = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      if (editing) {
        const input: UpdateUserInput = {
          displayName: values.displayName,
          email: values.email,
          tenantId: values.tenantId,
          namespaces: values.namespaces ?? [],
          disabled: values.disabled,
        };
        if (editing.authSource !== "oidc") input.role = values.role;
        if (editing.authSource !== "oidc" && values.password)
          input.password = values.password;
        const saved = await userService.update(editing.id, input);
        if (saved.id === session?.id) updateSession(saved);
        message.success(t("userUpdated"));
      } else {
        await userService.create(values);
        message.success(t("userCreated"));
      }
      setOpen(false);
      await load();
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };
  const remove = async (user: UserAccount) => {
    try {
      await userService.delete(user.id);
      message.success(t("userDeleted"));
      await load();
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
    }
  };
  const columns: ColumnsType<UserAccount> = [
    {
      title: t("username"),
      dataIndex: "username",
      width: 160,
      render: (value, row) => (
        <Space>
          <Typography.Text strong>{value}</Typography.Text>
          {row.id === session?.id && <Tag color="blue">{t("currentUser")}</Tag>}
        </Space>
      ),
    },
    { title: t("displayName"), dataIndex: "displayName", width: 180 },
    {
      title: t("email"),
      dataIndex: "email",
      ellipsis: true,
      render: (value) => value || "—",
    },
    {
      title: t("authSource"),
      dataIndex: "authSource",
      width: 100,
      render: (value) => (
        <Tag>{value === "oidc" ? "OIDC" : t("localAccount")}</Tag>
      ),
    },
    {
      title: t("role"),
      dataIndex: "role",
      width: 110,
      render: (value) => (
        <Tag color={value === "admin" ? "blue" : "default"}>{value}</Tag>
      ),
    },
    {
      title: en ? "Tenant" : "租户",
      dataIndex: "tenantId",
      width: 150,
      render: (value) =>
        tenants.find((x) => x.id === (value || "default"))?.name ||
        value ||
        "default",
    },
    {
      title: t("namespaceAccess"),
      dataIndex: "namespaces",
      width: 240,
      render: (value: string[]) =>
        value?.length ? (
          <Space size={[4, 4]} wrap>
            {value.map((namespace) => (
              <Tag key={namespace}>{namespace}</Tag>
            ))}
          </Space>
        ) : (
          <Tag color="blue">{t("allNamespaces")}</Tag>
        ),
    },
    {
      title: t("accountStatus"),
      dataIndex: "disabled",
      width: 110,
      render: (value) => (
        <Tag color={value ? "red" : "green"}>
          {t(value ? "disabled" : "active")}
        </Tag>
      ),
    },
    {
      title: t("createdAt"),
      dataIndex: "createdAt",
      width: 170,
      render: (value) => formatTimestamp(value),
    },
    {
      title: t("action"),
      key: "action",
      fixed: "right",
      width: 120,
      render: (_, row) => (
        <Space size={4}>
          <Button
            type="text"
            icon={<EditOutlined />}
            aria-label={t("editUser")}
            onClick={() => showEdit(row)}
          />
          <Popconfirm
            title={t("deleteUserConfirm")}
            okButtonProps={{ danger: true }}
            disabled={row.id === session?.id}
            onConfirm={() => remove(row)}
          >
            <Button
              danger
              type="text"
              disabled={row.id === session?.id}
              icon={<DeleteOutlined />}
              aria-label={t("deleteUser")}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];
  return (
    <>
      <PageHeader
        title={t("userManagement")}
        subtitle={t("userManagementHint")}
        extra={
          <Space>
            <Button onClick={() => setTenantsOpen(true)}>
              {en ? "Manage tenants" : "租户管理"}
            </Button>
            <Button icon={<ReloadOutlined />} onClick={load}>
              {t("refresh")}
            </Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={showCreate}>
              {t("createUser")}
            </Button>
          </Space>
        }
      />
      <Card className="panel-card">
        <Table
          rowKey="id"
          loading={loading}
          columns={columns}
          dataSource={rows}
          pagination={{ pageSize: 20 }}
          scroll={{ x: 1100 }}
        />
      </Card>
      <Modal
        title={t(editing ? "editUser" : "createUser")}
        open={open}
        confirmLoading={saving}
        onOk={save}
        onCancel={() => setOpen(false)}
        okText={t("save")}
        cancelText={t("cancel")}
        destroyOnHidden
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="username"
            label={t("username")}
            rules={[{ required: !editing }, { min: 3 }, { max: 64 }]}
          >
            <Input disabled={Boolean(editing)} autoComplete="off" />
          </Form.Item>
          <Form.Item name="displayName" label={t("displayName")}>
            <Input maxLength={100} />
          </Form.Item>
          <Form.Item
            name="email"
            label={t("email")}
            rules={[{ type: "email" }]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="role" label={t("role")} rules={[{ required: true }]}>
            <Select
              disabled={
                editing?.id === session?.id || editing?.authSource === "oidc"
              }
              options={[
                { value: "viewer", label: t("viewerRole") },
                { value: "admin", label: t("adminRole") },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="tenantId"
            label={en ? "Tenant" : "租户"}
            rules={[{ required: true }]}
            extra={
              en
                ? "Projects, workflows and task history are isolated by tenant, including administrators."
                : "项目、工作流及任务历史按租户隔离，管理员也遵守此边界。"
            }
          >
            <Select
              options={tenants.map((x) => ({ value: x.id, label: x.name }))}
            />
          </Form.Item>
          <Form.Item
            name="namespaces"
            label={t("namespaceAccess")}
            extra={t("namespaceAccessHint")}
          >
            <Select
              mode="multiple"
              allowClear
              options={runtimeConfig.cluster.namespaces.map((value) => ({
                value,
                label: value,
              }))}
              placeholder={t("allNamespaces")}
            />
          </Form.Item>
          {editing && (
            <Form.Item
              name="disabled"
              label={t("disabled")}
              valuePropName="checked"
            >
              <Switch disabled={editing.id === session?.id} />
            </Form.Item>
          )}
          {editing?.authSource !== "oidc" && (
            <Form.Item
              name="password"
              label={editing ? t("resetPasswordOptional") : t("password")}
              rules={
                editing
                  ? [{ min: 8 }]
                  : [{ required: true }, { min: 8 }, { max: 128 }]
              }
            >
              <Input.Password
                disabled={editing?.id === session?.id}
                autoComplete="new-password"
              />
            </Form.Item>
          )}
        </Form>
      </Modal>
      <Modal
        open={tenantsOpen}
        title={en ? "Tenant management" : "租户管理"}
        onCancel={() => setTenantsOpen(false)}
        footer={null}
      >
        <Button
          icon={<PlusOutlined />}
          onClick={() => setEditingTenant({ name: "" })}
        >
          {en ? "New tenant" : "创建租户"}
        </Button>
        <Table
          rowKey="id"
          dataSource={tenants}
          pagination={false}
          columns={[
            { title: en ? "Tenant" : "租户", dataIndex: "name" },
            { title: "ID", dataIndex: "id", ellipsis: true },
            {
              title: t("action"),
              render: (_, row: Tenant) => (
                <Button
                  size="small"
                  disabled={row.id === "default"}
                  onClick={() => setEditingTenant(row)}
                >
                  {t("editUser")}
                </Button>
              ),
            },
          ]}
        />
      </Modal>
      <Modal
        open={Boolean(editingTenant)}
        title={en ? "Save tenant" : "保存租户"}
        onCancel={() => setEditingTenant(undefined)}
        onOk={async () => {
          if (!editingTenant) return;
          try {
            await workflowService.saveTenant(editingTenant);
            setEditingTenant(undefined);
            await loadTenants();
          } catch (error) {
            message.error(String(error));
          }
        }}
      >
        <Input
          maxLength={100}
          value={editingTenant?.name}
          placeholder={en ? "Tenant name" : "租户名称"}
          onChange={(e) =>
            setEditingTenant({ ...editingTenant, name: e.target.value })
          }
        />
      </Modal>
    </>
  );
}
