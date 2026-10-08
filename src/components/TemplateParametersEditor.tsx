import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  Row,
  Select,
  Space,
  Switch,
  Typography,
} from "antd";
import type { TemplateParameter } from "../types";

export function TemplateParametersEditor({
  parameters,
  onChange,
  en,
  defaultsOnly = false,
}: {
  parameters: TemplateParameter[];
  onChange: (parameters: TemplateParameter[]) => void;
  en: boolean;
  defaultsOnly?: boolean;
}) {
  const update = (index: number, change: Partial<TemplateParameter>) =>
    onChange(parameters.map((p, i) => (i === index ? { ...p, ...change } : p)));
  return (
    <Space direction="vertical" style={{ width: "100%" }}>
      <Typography.Text type="secondary">
        {en
          ? 'Use {{name}} in the YAML. Quote standalone placeholders, for example: name: "{{name}}". Reference Kubernetes Secrets rather than adding secret parameters.'
          : '在 YAML 中使用 {{name}}；独立占位符请加引号，例如 name: "{{name}}"。敏感值应引用 Kubernetes Secret。'}
      </Typography.Text>
      {parameters.map((parameter, index) => (
        <Card
          size="small"
          key={index}
          title={`${en ? "Parameter" : "参数"} ${index + 1}`}
          extra={
            !defaultsOnly && (
              <Button
                type="text"
                danger
                aria-label={
                  en ? `Remove parameter ${index + 1}` : `删除参数 ${index + 1}`
                }
                icon={<DeleteOutlined />}
                onClick={() =>
                  onChange(parameters.filter((_, i) => i !== index))
                }
              />
            )
          }
        >
          <Row gutter={12}>
            <Col xs={24} sm={9}>
              <Form.Item label={en ? "Parameter name" : "参数名"} required>
                <Input
                  disabled={defaultsOnly}
                  placeholder="executorCount"
                  value={parameter.name}
                  onChange={(e) => update(index, { name: e.target.value })}
                />
              </Form.Item>
            </Col>
            <Col xs={16} sm={9}>
              <Form.Item label={en ? "Type" : "类型"}>
                <Select
                  disabled={defaultsOnly}
                  style={{ width: "100%" }}
                  value={parameter.type}
                  options={["string", "integer", "number", "boolean"].map(
                    (value) => ({ value, label: value }),
                  )}
                  onChange={(type) => update(index, { type })}
                />
              </Form.Item>
            </Col>
            <Col xs={8} sm={6}>
              <Form.Item label={en ? "Required" : "必填"}>
                <Switch
                  disabled={defaultsOnly}
                  checked={parameter.required}
                  onChange={(required) => update(index, { required })}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label={en ? "Default value" : "默认值"}>
                <Input
                  value={parameter.default}
                  onChange={(e) => update(index, { default: e.target.value })}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label={en ? "Allowed values" : "允许值"}>
                <Select
                  disabled={defaultsOnly}
                  mode="tags"
                  style={{ width: "100%" }}
                  value={parameter.enum || []}
                  placeholder={
                    en ? "Type a value and press Enter" : "输入值后按回车添加"
                  }
                  onChange={(values) => update(index, { enum: values })}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item
                label={en ? "Pattern (optional)" : "正则表达式（可选）"}
              >
                <Input
                  disabled={defaultsOnly}
                  value={parameter.pattern}
                  onChange={(e) => update(index, { pattern: e.target.value })}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label={en ? "Description" : "描述"}>
                <Input
                  disabled={defaultsOnly}
                  value={parameter.description}
                  onChange={(e) =>
                    update(index, { description: e.target.value })
                  }
                />
              </Form.Item>
            </Col>
          </Row>
        </Card>
      ))}
      {!defaultsOnly && (
        <Button
          icon={<PlusOutlined />}
          onClick={() =>
            onChange([
              ...parameters,
              { name: "", type: "string", required: false },
            ])
          }
        >
          {en ? "Add parameter" : "添加参数"}
        </Button>
      )}
    </Space>
  );
}
