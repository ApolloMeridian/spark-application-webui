import { FileAddOutlined, SafetyCertificateOutlined, SendOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Col, Input, Modal, Row, Select, Space, Tag, Typography } from 'antd';
import { useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { PageHeader } from '../components/PageHeader';
import { useI18n } from '../i18n';
import { runtimeConfig } from '../runtimeConfig';
import { sparkService } from '../service';
import type { ManifestPreview } from '../types';
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

interface PreparedLocationState { manifest?: string; namespace?: string; source?: string; mode?: 'clone' | 'retry' }

export function SubmitApplicationPage() {
  const { t } = useI18n();
  const { message } = App.useApp();
  const { session } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const prepared = (location.state ?? {}) as PreparedLocationState;
  const configuredNamespaces = runtimeConfig.cluster.namespaces;
  const namespaces = session?.namespaces?.length ? configuredNamespaces.filter((value) => session.namespaces.includes(value)) : configuredNamespaces;
  const initialNamespace = prepared.namespace && namespaces.includes(prepared.namespace) ? prepared.namespace : (namespaces[0] ?? 'spark');
  const [namespace, setNamespace] = useState(initialNamespace);
  const [manifest, setManifest] = useState(() => prepared.manifest ?? exampleManifest(initialNamespace));
  const [submitting, setSubmitting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [preview, setPreview] = useState<ManifestPreview>();
  const [validatedManifest, setValidatedManifest] = useState('');
  const [previewOpen, setPreviewOpen] = useState(false);
  const [error, setError] = useState('');
  const allowed = canSubmit(session?.role ?? 'viewer');
  const namespaceOptions = useMemo(() => namespaces.map((value) => ({ value, label: value })), [namespaces]);
  const validated = Boolean(preview?.dryRunAccepted && validatedManifest === manifest);

  const changeNamespace = (value: string) => {
    setNamespace(value); setPreview(undefined); setValidatedManifest('');
    setManifest((current) => current.replace(/(^\s*namespace:\s*)\S+/m, `$1${value}`));
  };
  const changeManifest = (value: string) => { setManifest(value); setPreview(undefined); setValidatedManifest(''); };
  const validate = async () => {
    setValidating(true); setError('');
    try {
      const result = await sparkService.dryRunApplication(namespace, manifest, session?.username ?? 'unknown');
      setPreview(result); setValidatedManifest(manifest); setPreviewOpen(true); message.success(t('dryRunSuccess'));
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'Validation failed'); }
    finally { setValidating(false); }
  };
  const submit = async () => {
    if (!validated) { setError(t('validationRequired')); return; }
    setSubmitting(true); setError('');
    try {
      const app = await sparkService.submitApplication(namespace, manifest, session?.username ?? 'unknown');
      message.success(t('submissionSuccess'));
      navigate(`/applications/${encodeURIComponent(app.namespace)}/${encodeURIComponent(app.name)}`);
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'Submission failed'); }
    finally { setSubmitting(false); }
  };

  if (!runtimeConfig.features.submit) return null;
  return <>
    <PageHeader title={t('submitApplication')} subtitle={t('yamlHint')} />
    {!allowed && <Alert className="detail-alert" type="warning" showIcon message={t('submitNoPermission')} />}
    {prepared.source && <Alert className="detail-alert" type="info" showIcon message={`${t('preparedFrom')}: ${prepared.source}`} description={prepared.mode === 'retry' ? t('retryApplication') : t('cloneApplication')} />}
    {error && <Alert className="detail-alert" type="error" showIcon closable onClose={() => setError('')} message={error} />}
    <Card className="panel-card submit-card">
      <div className="submit-toolbar">
        <Space wrap><Typography.Text strong>{t('namespace')}</Typography.Text><Select aria-label={t('namespace')} value={namespace} options={namespaceOptions} onChange={changeNamespace} style={{ minWidth: 220 }} /></Space>
        <Space wrap>
          {validated && <Tag color="success" icon={<SafetyCertificateOutlined />}>{t('dryRunSuccess')}</Tag>}
          <Button icon={<FileAddOutlined />} onClick={() => changeManifest(exampleManifest(namespace))}>{t('loadExample')}</Button>
          <Button icon={<SafetyCertificateOutlined />} loading={validating} disabled={!allowed || !manifest.trim()} onClick={validate}>{t('validateManifest')}</Button>
          <Button type="primary" icon={<SendOutlined />} loading={submitting} disabled={!allowed || !manifest.trim() || !validated} onClick={submit}>{t('submitToCluster')}</Button>
        </Space>
      </div>
      <Input.TextArea aria-label={t('yamlEditor')} className="yaml-editor" value={manifest} onChange={(event) => changeManifest(event.target.value)} spellCheck={false} autoSize={{ minRows: 24, maxRows: 36 }} />
    </Card>
    <Modal title={t('manifestDiff')} open={previewOpen} onCancel={() => setPreviewOpen(false)} width="min(1280px, 94vw)" footer={<Button type="primary" onClick={() => setPreviewOpen(false)}>OK</Button>}>
      {preview?.warnings.map((warning) => <Alert key={warning} type="warning" showIcon message={warning} className="detail-alert" />)}
      <Row gutter={[16, 16]}><Col xs={24} xl={12}><Typography.Title level={5}>{t('originalManifest')}</Typography.Title><pre className="manifest-diff">{preview?.originalYaml}</pre></Col><Col xs={24} xl={12}><Typography.Title level={5}>{t('serverManifest')}</Typography.Title><pre className="manifest-diff accepted">{preview?.serverYaml}</pre></Col></Row>
    </Modal>
  </>;
}
