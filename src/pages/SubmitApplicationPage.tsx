import { FileAddOutlined, SendOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Input, Select, Space, Typography } from 'antd';
import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { PageHeader } from '../components/PageHeader';
import { useI18n } from '../i18n';
import { runtimeConfig } from '../runtimeConfig';
import { sparkService } from '../service';
import { canSubmit } from '../utils';

function exampleManifest(namespace: string) {
  return `apiVersion: sparkoperator.k8s.io/v1beta2
kind: SparkApplication
metadata:
  name: spark-pi-example
  namespace: ${namespace}
spec:
  type: Scala
  mode: cluster
  image: spark:3.5.3
  imagePullPolicy: IfNotPresent
  mainClass: org.apache.spark.examples.SparkPi
  mainApplicationFile: local:///opt/spark/examples/jars/spark-examples_2.12-3.5.3.jar
  sparkVersion: 3.5.3
  restartPolicy:
    type: Never
  driver:
    cores: 1
    memory: 1g
    serviceAccount: spark
  executor:
    instances: 2
    cores: 1
    memory: 1g
`;
}

export function SubmitApplicationPage() {
  const { t } = useI18n();
  const { message } = App.useApp();
  const { session } = useAuth();
  const navigate = useNavigate();
  const namespaces = runtimeConfig.cluster.namespaces;
  const [namespace, setNamespace] = useState(namespaces[0] ?? 'spark');
  const [manifest, setManifest] = useState(() => exampleManifest(namespaces[0] ?? 'spark'));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const allowed = canSubmit(session?.role ?? 'viewer');
  const namespaceOptions = useMemo(() => namespaces.map((value) => ({ value, label: value })), [namespaces]);

  const changeNamespace = (value: string) => {
    setNamespace(value);
    setManifest((current) => current.replace(/(^\s*namespace:\s*)\S+/m, `$1${value}`));
  };
  const submit = async () => {
    setSubmitting(true); setError('');
    try {
      const app = await sparkService.submitApplication(namespace, manifest, session?.username ?? 'unknown');
      message.success(t('submissionSuccess'));
      navigate(`/applications/${encodeURIComponent(app.namespace)}/${encodeURIComponent(app.name)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Submission failed');
    } finally {
      setSubmitting(false);
    }
  };

  if (!runtimeConfig.features.submit) return null;
  return <>
    <PageHeader title={t('submitApplication')} subtitle={t('yamlHint')} />
    {!allowed && <Alert className="detail-alert" type="warning" showIcon message={t('submitNoPermission')} />}
    {error && <Alert className="detail-alert" type="error" showIcon closable onClose={() => setError('')} message={error} />}
    <Card className="panel-card submit-card">
      <div className="submit-toolbar">
        <Space wrap>
          <Typography.Text strong>{t('namespace')}</Typography.Text>
          <Select aria-label={t('namespace')} value={namespace} options={namespaceOptions} onChange={changeNamespace} style={{ minWidth: 220 }} />
        </Space>
        <Space wrap>
          <Button icon={<FileAddOutlined />} onClick={() => setManifest(exampleManifest(namespace))}>{t('loadExample')}</Button>
          <Button type="primary" icon={<SendOutlined />} loading={submitting} disabled={!allowed || !manifest.trim()} onClick={submit}>{t('submitToCluster')}</Button>
        </Space>
      </div>
      <Input.TextArea aria-label={t('yamlEditor')} className="yaml-editor" value={manifest} onChange={(event) => setManifest(event.target.value)} spellCheck={false} autoSize={{ minRows: 24, maxRows: 36 }} />
    </Card>
  </>;
}
