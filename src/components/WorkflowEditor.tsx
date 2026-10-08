import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Select,
  Row,
  Space,
  Switch,
  Tabs,
  Tag,
  Typography,
} from "antd";
import { useState } from "react";
import { WorkflowGraph } from "./WorkflowGraph";
export { WorkflowGraph } from "./WorkflowGraph";
import { stringify, parse } from "yaml";
import { TemplateParametersEditor } from "./TemplateParametersEditor";
import type { ApplicationTemplate } from "../types";
import {
  newNode,
  validateDAG,
  type Workflow,
  type WorkflowNode,
} from "../workflows";

export function strategyOptions(en: boolean) {
  return [
    { value: "parallel", label: en ? "Parallel" : "并行" },
    { value: "serial_wait", label: en ? "Serial wait" : "串行等待" },
    { value: "serial_discard", label: en ? "Serial discard" : "串行抛弃" },
  ];
}

export function WorkflowEditor({
  value,
  onChange,
  templates,
  namespaces,
  en,
  onValidityChange,
}: {
  value: Workflow;
  onChange: (value: Workflow) => void;
  templates: ApplicationTemplate[];
  namespaces: string[];
  en: boolean;
  onValidityChange?: (valid: boolean) => void;
}) {
  const [selected, setSelected] = useState(value.nodes[0]?.id);
  const [valuesText, setValuesText] = useState<string>();
  const [valuesError, setValuesError] = useState("");
  const node = value.nodes.find((n) => n.id === selected);
  const updateNode = (change: Partial<WorkflowNode>) =>
    onChange({
      ...value,
      nodes: value.nodes.map((n) =>
        n.id === selected ? { ...n, ...change } : n,
      ),
    });
  const update = (change: Partial<Workflow>) =>
    onChange({ ...value, ...change });
  const error = validateDAG(value.nodes);
  const chooseNode = (key: string) => {
    setSelected(key);
    setValuesText(undefined);
    setValuesError("");
  };
  return (
    <Form layout="vertical">
      <Row gutter={16}>
        <Col xs={24} md={9}>
          <Form.Item label={en ? "Workflow name" : "工作流名称"} required>
            <Input
              value={value.name}
              maxLength={100}
              onChange={(e) => update({ name: e.target.value })}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={5}>
          <Form.Item label={en ? "Concurrency strategy" : "并发策略"}>
            <Select
              value={value.strategy}
              options={strategyOptions(en)}
              onChange={(strategy) => update({ strategy })}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={5}>
          <Form.Item label={en ? "Time zone" : "时区"}>
            <Input
              value={value.timeZone}
              onChange={(e) => update({ timeZone: e.target.value })}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={5}>
          <Form.Item label={en ? "Enabled" : "启用"}>
            <Switch
              checked={value.enabled}
              onChange={(enabled) => update({ enabled })}
            />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={16}>
        <Col xs={24} md={14}>
          <Form.Item label={en ? "Description" : "描述"}>
            <Input
              value={value.description}
              onChange={(e) => update({ description: e.target.value })}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={5}>
          <Form.Item label={en ? "Parallel tasks" : "并行任务上限"}>
            <InputNumber
              min={1}
              max={100}
              value={value.maxParallel}
              onChange={(v) => update({ maxParallel: v || 1 })}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={5}>
          <Form.Item
            label={en ? "Timeout (minutes, 0 = off)" : "超时（分钟，0 为关闭）"}
          >
            <InputNumber
              min={0}
              value={value.timeoutMinutes}
              onChange={(v) => update({ timeoutMinutes: v || 0 })}
            />
          </Form.Item>
        </Col>
      </Row>
      {error && (
        <Alert
          type="error"
          message={
            en ? error : "存在循环依赖、缺失任务或重复 ID，请检查依赖配置。"
          }
          showIcon
          style={{ marginBottom: 12 }}
        />
      )}
      <Card
        size="small"
        title={en ? "Task dependencies" : "任务依赖图"}
        extra={
          <Button
            size="small"
            icon={<PlusOutlined />}
            onClick={() => {
              const n = newNode(namespaces[0] || "", value.nodes.length);
              update({ nodes: [...value.nodes, n] });
              chooseNode(n.id);
            }}
          >
            {en ? "Add task" : "添加任务"}
          </Button>
        }
      >
        <WorkflowGraph
          nodes={value.nodes}
          onChange={(nodes) => update({ nodes })}
          selected={selected}
          onSelect={chooseNode}
          en={en}
        />
        <Select
          style={{ width: "100%", marginTop: 10 }}
          value={selected}
          options={value.nodes.map((n) => ({ value: n.id, label: n.name }))}
          onChange={chooseNode}
          aria-label={en ? "Select task" : "选择任务"}
        />
      </Card>
      {node && (
        <Card
          size="small"
          style={{ marginTop: 14 }}
          title={
            <Space>
              {en ? "Task configuration" : "任务配置"}
              <Tag>{node.id}</Tag>
            </Space>
          }
          extra={
            <Button
              danger
              size="small"
              disabled={value.nodes.length === 1}
              icon={<DeleteOutlined />}
              onClick={() => {
                const nodes = value.nodes
                  .filter((n) => n.id !== node.id)
                  .map((n) => ({
                    ...n,
                    dependsOn: n.dependsOn.filter((d) => d !== node.id),
                  }));
                update({ nodes });
                chooseNode(nodes[0].id);
              }}
            >
              {en ? "Remove task" : "移除任务"}
            </Button>
          }
        >
          <Row gutter={16}>
            <Col xs={24} md={8}>
              <Form.Item label={en ? "Task name" : "任务名称"} required>
                <Input
                  value={node.name}
                  onChange={(e) => updateNode({ name: e.target.value })}
                />
              </Form.Item>
            </Col>
            <Col xs={12} md={8}>
              <Form.Item label={en ? "Namespace" : "命名空间"} required>
                <Select
                  value={node.namespace}
                  options={namespaces.map((ns) => ({ value: ns, label: ns }))}
                  onChange={(namespace) =>
                    updateNode({
                      namespace,
                      templateId: "",
                      templateVersion: undefined,
                    })
                  }
                />
              </Form.Item>
            </Col>
            <Col xs={12} md={8}>
              <Form.Item
                label={en ? "Across-instance strategy" : "跨实例并发策略"}
              >
                <Select
                  value={node.strategy}
                  options={strategyOptions(en)}
                  onChange={(strategy) => updateNode({ strategy })}
                />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item label={en ? "Wait for tasks" : "等待上游任务"}>
            <Select
              mode="multiple"
              value={node.dependsOn}
              options={value.nodes
                .filter((n) => n.id !== node.id)
                .map((n) => ({ value: n.id, label: n.name }))}
              onChange={(dependsOn) => updateNode({ dependsOn })}
            />
          </Form.Item>
          <Row gutter={16}>
            <Col xs={12} md={8}>
              <Form.Item
                label={
                  en ? "Unique application name suffix" : "作业名称唯一后缀"
                }
                extra={
                  en
                    ? "Time + instance ID + attempt. Off: CREATE conflicts fail, never overwrite."
                    : "时间 + 实例标识 + 尝试号。关闭时，同名 CREATE 冲突将失败，不会覆盖。"
                }
              >
                <Switch
                  checked={node.nameSuffix !== false}
                  onChange={(nameSuffix) => updateNode({ nameSuffix })}
                />
              </Form.Item>
            </Col>
            <Col xs={12} md={8}>
              <Form.Item label={en ? "Retries" : "重试次数"}>
                <InputNumber
                  min={0}
                  max={10}
                  value={node.retries}
                  onChange={(v) => updateNode({ retries: v || 0 })}
                />
              </Form.Item>
            </Col>
            <Col xs={12} md={8}>
              <Form.Item
                label={en ? "Task timeout (minutes)" : "任务超时（分钟）"}
              >
                <InputNumber
                  min={0}
                  value={node.timeoutMinutes}
                  onChange={(v) => updateNode({ timeoutMinutes: v || 0 })}
                />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            label={en ? "Source template (optional)" : "来源模板（可选）"}
            extra={
              en
                ? "Saved workflows freeze the selected template version. Clear to edit independent YAML."
                : "保存工作流时冻结选中的模板版本；清空后可编辑独立 YAML。"
            }
          >
            <Select
              allowClear
              value={node.templateId || undefined}
              options={templates
                .filter((t) => t.namespace === node.namespace && !t.disabled)
                .map((t) => ({
                  value: t.id,
                  label: `${t.name} · v${t.version}`,
                }))}
              onChange={(templateId) => {
                const t = templates.find((t) => t.id === templateId);
                updateNode({
                  templateId: templateId || "",
                  templateVersion: t?.version,
                  ...(t
                    ? { manifest: t.manifest, parameters: t.parameters }
                    : {}),
                });
              }}
            />
          </Form.Item>
          {node.templateId && (
            <Form.Item label={en ? "Template version" : "模板版本"}>
              <InputNumber
                min={1}
                value={node.templateVersion}
                onChange={(v) => updateNode({ templateVersion: v || 1 })}
              />
            </Form.Item>
          )}
          <Tabs
            items={[
              {
                key: "yaml",
                label: "SparkApplication YAML",
                children: (
                  <Input.TextArea
                    className="yaml-editor"
                    readOnly={Boolean(node.templateId)}
                    value={node.manifest}
                    autoSize={{ minRows: 10, maxRows: 26 }}
                    onChange={(e) => updateNode({ manifest: e.target.value })}
                  />
                ),
              },
              {
                key: "params",
                label: en ? "Parameter definitions" : "参数定义",
                children: node.templateId ? (
                  <Input.TextArea
                    readOnly
                    className="yaml-editor"
                    value={stringify(node.parameters)}
                    autoSize={{ minRows: 4, maxRows: 20 }}
                  />
                ) : (
                  <TemplateParametersEditor
                    en={en}
                    parameters={node.parameters}
                    onChange={(parameters) => updateNode({ parameters })}
                  />
                ),
              },
              {
                key: "values",
                label: en ? "Runtime values (YAML)" : "运行参数（YAML）",
                children: (
                  <>
                    <Alert
                      type="info"
                      showIcon
                      message={
                        en
                          ? "Use key: value. Date expressions use the logical schedule time, e.g. $[yyyy-MM-dd-1] or $[yyyy-MM-dd HH:mm:ss-1/24]."
                          : "使用 key: value；日期表达式以计划调度时间为基准，例如 $[yyyy-MM-dd-1] 或 $[yyyy-MM-dd HH:mm:ss-1/24]。"
                      }
                      style={{ marginBottom: 10 }}
                    />
                    <Input.TextArea
                      className="yaml-editor"
                      value={valuesText ?? stringify(node.values)}
                      autoSize={{ minRows: 5, maxRows: 12 }}
                      onChange={(e) => {
                        setValuesText(e.target.value);
                        try {
                          const parsed: unknown = parse(e.target.value) || {};
                          if (
                            !parsed ||
                            typeof parsed !== "object" ||
                            Array.isArray(parsed)
                          )
                            throw new Error(
                              en
                                ? "Values must be a mapping"
                                : "参数必须为键值映射",
                            );
                          const values = Object.fromEntries(
                            Object.entries(parsed).map(([key, v]) => [
                              key,
                              String(v),
                            ]),
                          );
                          updateNode({ values });
                          setValuesError("");
                          onValidityChange?.(true);
                        } catch (e) {
                          setValuesError(String(e));
                          onValidityChange?.(false);
                        }
                      }}
                    />
                    {valuesError && (
                      <Alert type="error" message={valuesError} />
                    )}
                  </>
                ),
              },
            ]}
          />
        </Card>
      )}
    </Form>
  );
}
