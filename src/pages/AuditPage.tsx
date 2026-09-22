import { ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Input, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { PageHeader } from '../components/PageHeader';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import { runtimeConfig } from '../runtimeConfig';
import type { OperationAudit } from '../types';
import { formatTimestamp } from '../utils';

export function AuditPage() {
  const { session } = useAuth(); const { t } = useI18n(); const [rows, setRows] = useState<OperationAudit[]>([]); const [loading, setLoading] = useState(true); const [query, setQuery] = useState(''); const [error, setError] = useState('');
  const load = useCallback(async () => { setLoading(true); setError(''); try { setRows(await sparkService.getAudit()); } catch (e) { setError(e instanceof Error ? e.message : 'Failed to load'); } finally { setLoading(false); } }, []);
  useEffect(() => { void load(); }, [load]);
  const filtered = useMemo(() => rows.filter((row) => [row.applicationName, row.operator, row.reason ?? '', row.message].some((v) => v.toLowerCase().includes(query.toLowerCase()))), [rows, query]);
  if (!runtimeConfig.features.audit || session?.role !== 'admin') return <Navigate to="/overview" replace />;
  const columns: ColumnsType<OperationAudit> = [
    { title: t('timestamp'), dataIndex: 'timestamp', width: 190, render: (v) => formatTimestamp(v) },
    { title: t('application'), dataIndex: 'applicationName', width: 220, render: (v, row) => <div><b>{v}</b><small className="cell-subtitle">{row.namespace}</small></div> },
    { title: t('auditOperator'), dataIndex: 'operator', width: 130 }, { title: t('operation'), dataIndex: 'operation', width: 110, render: (v) => <Tag color={v === 'SUBMIT' ? 'blue' : v === 'KILL' ? 'orange' : 'red'}>{v}</Tag> },
    { title: t('result'), dataIndex: 'result', width: 110, render: (v) => <Tag color={v === 'SUCCESS' ? 'success' : 'error'}>{v}</Tag> }, { title: t('reason'), dataIndex: 'reason', render: (v) => v || '—' }, { title: t('message'), dataIndex: 'message', ellipsis: true },
  ];
  return <><PageHeader title={t('audit')} subtitle="Operator action history" extra={<Button icon={<ReloadOutlined />} onClick={load}>{t('refresh')}</Button>} /><Card className="filter-card"><Input prefix={<SearchOutlined />} value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search operator, application, reason" className="search-input" /></Card>{error ? <Alert type="error" message={error} /> : <Card className="table-card"><Table rowKey="id" loading={loading} dataSource={filtered} columns={columns} scroll={{ x: 1100 }} locale={{ emptyText: <div className="audit-empty"><Typography.Title level={4}>No operator actions yet</Typography.Title><Typography.Text type="secondary">Kill an active application to generate an audit record.</Typography.Text></div> }} /></Card>}</>;
}
