import { Alert, Button, Skeleton } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { PageHeader } from '../components/PageHeader';
import { useI18n } from '../i18n';
import { apiUrl } from '../runtimeConfig';
import { sparkService } from '../service';
import type { SparkApplication } from '../types';

export function SparkUIPage() {
  const { namespace = '', name = '' } = useParams();
  const { t } = useI18n();
  const [app, setApp] = useState<SparkApplication>();
  const [error, setError] = useState('');
  const load = useCallback(async () => {
    setError('');
    try { setApp(await sparkService.getApplication(namespace, name)); }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Failed to load application'); }
  }, [namespace, name]);
  useEffect(() => { void load(); }, [load]);
  if (error) return <Alert type="error" showIcon message={error} action={<Button onClick={load}>{t('retry')}</Button>} />;
  if (!app) return <Skeleton active paragraph={{ rows: 10 }} />;
  if (app.state !== 'RUNNING' || !app.sparkUiAvailable) return <Alert type="warning" showIcon message={t('sparkUiUnavailable')} />;
  const source = apiUrl(`/v1/namespaces/${encodeURIComponent(namespace)}/applications/${encodeURIComponent(name)}/spark-ui/`);
  return <>
    <PageHeader back={{ label: app.name, to: `/applications/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}` }} title={`${app.name} · Spark UI`} subtitle={t('sparkUiProxyHint')} />
    <div className="spark-ui-frame-wrap"><iframe title={`${app.name} Spark UI`} src={source} className="spark-ui-frame" sandbox="allow-forms allow-modals allow-popups allow-same-origin allow-scripts" /></div>
  </>;
}
