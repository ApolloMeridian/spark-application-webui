import { DeleteOutlined, EyeOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Input, Progress, Select, Space, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType, TableProps } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { KillModal } from '../components/KillModal';
import { PageHeader } from '../components/PageHeader';
import { StatusTag } from '../components/StatusTag';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import { runtimeConfig } from '../runtimeConfig';
import type { ApplicationState, SparkApplication } from '../types';
import { canKill, formatCpu, formatDuration, formatMemory, sumResources } from '../utils';

export function ApplicationsPage() {
  const { t } = useI18n(); const { session } = useAuth(); const navigate = useNavigate(); const location = useLocation();
  const initialState = new URLSearchParams(location.search).get('state') as ApplicationState | null;
  const [apps, setApps] = useState<SparkApplication[]>([]); const [loading, setLoading] = useState(true); const [error, setError] = useState('');
  const [keyword, setKeyword] = useState(() => localStorage.getItem('spark-console-filter-keyword') || '');
  const [state, setState] = useState<ApplicationState | undefined>(initialState || undefined); const [namespace, setNamespace] = useState<string>(); const [target, setTarget] = useState<SparkApplication>();
  const load = useCallback(async () => { setLoading(true); setError(''); try { setApps(await sparkService.listApplications()); } catch (e) { setError(e instanceof Error ? e.message : 'Failed to load'); } finally { setLoading(false); } }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { localStorage.setItem('spark-console-filter-keyword', keyword); }, [keyword]);
  const filtered = useMemo(() => apps.filter((app) => (!keyword.trim() || [app.name, app.owner, app.team].some((v) => v.toLowerCase().includes(keyword.toLowerCase()))) && (!state || app.state === state) && (!namespace || app.namespace === namespace)), [apps, keyword, state, namespace]);
  const columns: ColumnsType<SparkApplication> = [
    { title: t('application'), dataIndex: 'name', width: 230, sorter: (a, b) => a.name.localeCompare(b.name), render: (value, row) => <div className="app-cell"><span className="spark-mini-mark">S</span><span><Typography.Link onClick={() => navigate(`/applications/${row.namespace}/${row.name}`)}>{value}</Typography.Link><small>{row.namespace} · {row.team}</small></span></div> },
    { title: t('owner'), dataIndex: 'owner', width: 105, sorter: (a, b) => a.owner.localeCompare(b.owner) },
    { title: t('status'), dataIndex: 'state', width: 160, filters: ['RUNNING', 'COMPLETED', 'FAILED', 'PENDING'].map((value) => ({ text: value, value })), onFilter: (value, row) => row.state === value, render: (value) => <StatusTag state={value} /> },
    { title: t('duration'), width: 100, sorter: (a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime(), render: (_, row) => formatDuration(row) },
    { title: t('driver'), width: 125, render: (_, row) => <div className="resource-cell"><b>{formatCpu(row.driver.request.cpu)}</b><small>{formatMemory(row.driver.request.memoryGiB)}</small></div> },
    { title: t('executors'), width: 105, align: 'center', render: (_, row) => <b>{row.executors.length}</b> },
    { title: `${t('cpu')} · used/request`, width: 190, render: (_, row) => { const value = sumResources(row); const percent = Math.min(100, Math.round(value.used.cpu / value.requested.cpu * 100)); return <div className="table-progress"><span>{formatCpu(value.used.cpu)} / {formatCpu(value.requested.cpu)}</span><Progress size="small" percent={percent || 0} showInfo={false} /></div>; } },
    { title: `${t('memory')} · used/request`, width: 205, render: (_, row) => { const value = sumResources(row); const percent = Math.min(100, Math.round(value.used.memoryGiB / value.requested.memoryGiB * 100)); return <div className="table-progress"><span>{formatMemory(value.used.memoryGiB)} / {formatMemory(value.requested.memoryGiB)}</span><Progress size="small" percent={percent || 0} showInfo={false} strokeColor="#7a58e8" /></div>; } },
    { title: t('action'), key: 'action', width: 110, fixed: 'right', render: (_, row) => <Space><Tooltip title={t('details')}><Button size="small" type="text" icon={<EyeOutlined />} onClick={() => navigate(`/applications/${row.namespace}/${row.name}`)} /></Tooltip>{runtimeConfig.features.kill && ['RUNNING', 'PENDING', 'SUBMITTED', 'FAILING'].includes(row.state) && <Tooltip title={canKill(session?.role ?? 'viewer') ? t('kill') : t('noPermission')}><Button size="small" danger type="text" disabled={!canKill(session?.role ?? 'viewer')} icon={<DeleteOutlined />} onClick={() => setTarget(row)} /></Tooltip>}</Space> },
  ];
  const onChange: TableProps<SparkApplication>['onChange'] = () => undefined;
  return <>
    <PageHeader title={t('applications')} subtitle={`${filtered.length} applications across ${runtimeConfig.cluster.namespaces.length} namespaces`} extra={<Button icon={<ReloadOutlined />} onClick={load}>{t('refresh')}</Button>} />
    <Card className="filter-card"><div className="filter-bar"><Input allowClear value={keyword} onChange={(e) => setKeyword(e.target.value)} prefix={<SearchOutlined />} placeholder={t('search')} className="search-input" /><Select allowClear value={state} onChange={setState} placeholder={t('status')} options={['RUNNING', 'COMPLETED', 'FAILED', 'SUBMISSION_FAILED', 'PENDING', 'SUBMITTED', 'FAILING', 'UNKNOWN', 'KILLED'].map((value) => ({ value, label: value }))} /><Select allowClear value={namespace} onChange={setNamespace} placeholder={t('namespace')} options={[...new Set(apps.map((app) => app.namespace))].map((value) => ({ value, label: value }))} /><Button onClick={() => { setKeyword(''); setState(undefined); setNamespace(undefined); }}>{t('reset')}</Button></div></Card>
    {error ? <Alert type="error" showIcon message={error} action={<Button onClick={load}>{t('retry')}</Button>} /> : <Card className="table-card"><Table rowKey="id" loading={loading} dataSource={filtered} columns={columns} onChange={onChange} scroll={{ x: 1450 }} pagination={{ pageSize: 8, showSizeChanger: true, showTotal: (total) => `${total} applications` }} /></Card>}
    {target && <KillModal application={target} open onClose={() => setTarget(undefined)} onKilled={() => { void load(); }} />}
  </>;
}
