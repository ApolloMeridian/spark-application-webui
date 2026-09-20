import { CopyOutlined, DeleteOutlined, DownloadOutlined, ExclamationCircleOutlined, LinkOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Col, Descriptions, Empty, Input, Progress, Row, Segmented, Skeleton, Space, Table, Tabs, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import ReactECharts from 'echarts-for-react';
import dayjs from 'dayjs';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useAuth } from '../auth';
import { KillModal } from '../components/KillModal';
import { PageHeader } from '../components/PageHeader';
import { StatusTag } from '../components/StatusTag';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import { runtimeConfig } from '../runtimeConfig';
import type { ExecutorPod, KubernetesEvent, SparkApplication } from '../types';
import { canKill, formatCpu, formatDuration, formatMemory, sumResources } from '../utils';

function ResourceSummary({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const totals = sumResources(app);
  const rows = [
    { label: 'Driver CPU', request: formatCpu(app.driver.request.cpu), current: formatCpu(app.driver.current?.cpu), peak: formatCpu(app.driver.peak?.cpu), color: '#2868f0' },
    { label: 'Driver memory', request: formatMemory(app.driver.request.memoryGiB), current: formatMemory(app.driver.current?.memoryGiB), peak: formatMemory(app.driver.peak?.memoryGiB), color: '#7a58e8' },
    { label: 'Executor CPU', request: formatCpu(totals.requested.cpu - app.driver.request.cpu), current: formatCpu(totals.used.cpu - (app.driver.current?.cpu ?? 0)), peak: formatCpu(app.executors.reduce((s, e) => s + (e.resources.peak?.cpu ?? 0), 0)), color: '#2868f0' },
    { label: 'Executor memory', request: formatMemory(totals.requested.memoryGiB - app.driver.request.memoryGiB), current: formatMemory(totals.used.memoryGiB - (app.driver.current?.memoryGiB ?? 0)), peak: formatMemory(app.executors.reduce((s, e) => s + (e.resources.peak?.memoryGiB ?? 0), 0)), color: '#7a58e8' },
  ];
  return <Card className="panel-card resource-summary-card" title={t('resources')}><div className="resource-summary-head"><span>Component</span><span>{t('request')}</span><span>{t('current')}</span><span>{t('peak')}</span></div>{rows.map((row) => <div className="resource-summary-row" key={row.label}><b>{row.label}</b><span>{row.request}</span><span>{row.current}</span><span>{row.peak}</span><i style={{ background: row.color }} /></div>)}</Card>;
}

function ResourcesTab({ app }: { app: SparkApplication }) {
  const [metric, setMetric] = useState<'cpu' | 'memoryGiB'>('cpu');
  const option = useMemo(() => ({
    animation: false,
    tooltip: { trigger: 'axis' }, grid: { top: 28, left: 52, right: 24, bottom: 42 },
    xAxis: { type: 'category', boundaryGap: false, data: app.metrics.map((p) => dayjs(p.time).format('HH:mm')), axisLine: { lineStyle: { color: '#dce2eb' } }, axisLabel: { color: '#7d8799' } },
    yAxis: { type: 'value', name: metric === 'cpu' ? 'cores' : 'GiB', splitLine: { lineStyle: { color: '#edf1f6' } } },
    series: [{ name: metric === 'cpu' ? 'CPU used' : 'Memory used', type: 'line', smooth: true, showSymbol: false, data: app.metrics.map((p) => p[metric]), lineStyle: { width: 3, color: metric === 'cpu' ? '#2868f0' : '#7a58e8' }, areaStyle: { color: metric === 'cpu' ? 'rgba(40,104,240,.11)' : 'rgba(122,88,232,.11)' } }],
  }), [app.metrics, metric]);
  return <div className="tab-stack"><ResourceSummary app={app} /><Card className="panel-card" title="Historical usage · last 50 min" extra={<Segmented value={metric} onChange={(value) => setMetric(value as 'cpu' | 'memoryGiB')} options={[{ value: 'cpu', label: 'CPU' }, { value: 'memoryGiB', label: 'Memory' }]} />}>{app.metrics.length ? <ReactECharts option={option} style={{ height: 310 }} /> : <Empty description="No time-series metrics for this application" />}</Card></div>;
}

function ExecutorsTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n();
  const columns: ColumnsType<ExecutorPod> = [
    { title: 'Pod', dataIndex: 'name', width: 260, render: (value) => <Typography.Text copyable={{ text: value }}>{value}</Typography.Text> },
    { title: t('status'), dataIndex: 'state', width: 110, render: (value) => <Tag color={value === 'RUNNING' ? 'success' : value === 'PENDING' ? 'warning' : value === 'FAILED' ? 'error' : 'blue'}>{value}</Tag> },
    { title: t('cpu'), width: 180, render: (_, row) => <div className="table-progress"><span>{formatCpu(row.resources.current?.cpu)} / {formatCpu(row.resources.request.cpu)}</span><Progress size="small" percent={Math.round((row.resources.current?.cpu ?? 0) / row.resources.request.cpu * 100)} showInfo={false} /></div> },
    { title: t('memory'), width: 190, render: (_, row) => <div className="table-progress"><span>{formatMemory(row.resources.current?.memoryGiB)} / {formatMemory(row.resources.request.memoryGiB)}</span><Progress size="small" percent={Math.round((row.resources.current?.memoryGiB ?? 0) / row.resources.request.memoryGiB * 100)} showInfo={false} strokeColor="#7a58e8" /></div> },
    { title: t('node'), dataIndex: 'node', width: 165, render: (value) => value ?? <Typography.Text type="warning">Unscheduled</Typography.Text> },
    { title: t('nodePool'), dataIndex: 'nodePool', width: 110, render: (value) => value ? <Tag color={value === 'autoscale' ? 'green' : 'blue'}>{value}</Tag> : '—' },
    { title: t('restarts'), dataIndex: 'restarts', align: 'center', width: 90 },
  ];
  return <Card className="panel-card no-padding"><Table rowKey="name" columns={columns} dataSource={app.executors} pagination={false} scroll={{ x: 1150 }} /></Card>;
}

function SchedulingTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const baseline = app.executors.filter((e) => e.nodePool === 'baseline' && e.node).length; const autoscale = app.executors.filter((e) => e.nodePool === 'autoscale' && e.node).length; const total = Math.max(1, baseline + autoscale);
  return <Row gutter={[16, 16]}><Col xs={24} lg={10}><Card className="panel-card" title={t('scheduling')}><div className="driver-location"><small>DRIVER</small><b>{app.driverPod}</b><span>{app.driverNode ?? 'Unscheduled'}</span><Tag color="blue">{app.driverNodePool ?? 'pending'}</Tag></div><div className="pool-row"><span><i className="pool-dot baseline" />Baseline pool</span><b>{baseline} executors</b></div><Progress percent={Math.round(baseline / total * 100)} showInfo={false} strokeColor="#2868f0" /><div className="pool-row"><span><i className="pool-dot autoscale" />Autoscale pool</span><b>{autoscale} executors</b></div><Progress percent={Math.round(autoscale / total * 100)} showInfo={false} strokeColor="#12a878" /></Card></Col><Col xs={24} lg={14}><Card className="panel-card" title="Scheduler diagnosis">{app.pendingReason ? <Alert type="warning" showIcon message="FailedScheduling" description={app.pendingReason} /> : <Alert type="success" showIcon message="All requested pods are scheduled" description="The driver and all executors have an assigned Kubernetes node." />}<div className="node-list">{app.executors.map((executor) => <div key={executor.name}><span className={`node-state ${executor.node ? 'ok' : 'pending'}`} /><b>{executor.name.replace(`${app.name}-`, '')}</b><span>{executor.node ?? 'Waiting for node'}</span><Tag>{executor.nodePool ?? 'pending'}</Tag></div>)}</div></Card></Col></Row>;
}

function LogsTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const { message } = App.useApp(); const [query, setQuery] = useState(''); const [level, setLevel] = useState('ALL');
  const lines = app.logs.filter((line) => (level === 'ALL' || line.includes(` ${level} `)) && line.toLowerCase().includes(query.toLowerCase()));
  const copy = async () => { await navigator.clipboard.writeText(lines.join('\n')); message.success(t('copied')); };
  return <Card className="panel-card logs-card" title={<Space><span>{app.driverPod}</span><Tag>stdout</Tag></Space>} extra={<Space><Input prefix={<SearchOutlined />} value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Filter logs" /><Segmented value={level} onChange={(v) => setLevel(String(v))} options={['ALL', 'INFO', 'WARN', 'ERROR']} /><Button icon={<CopyOutlined />} onClick={copy}>{t('copy')}</Button></Space>}><pre className="log-viewer">{lines.length ? lines.map((line, index) => <div className={line.includes('ERROR') ? 'log-error' : line.includes('WARN') ? 'log-warn' : ''} key={index}><span>{String(index + 1).padStart(2, '0')}</span>{line}</div>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />}</pre></Card>;
}

function EventsTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const columns: ColumnsType<KubernetesEvent> = [
    { title: 'Type', dataIndex: 'type', width: 110, render: (v) => <Tag color={v === 'Warning' ? 'warning' : 'blue'}>{v}</Tag> }, { title: 'Reason', dataIndex: 'reason', width: 160 },
    { title: t('message'), dataIndex: 'message' }, { title: 'Source', dataIndex: 'source', width: 180 }, { title: 'Count', dataIndex: 'count', width: 80, align: 'center' }, { title: t('timestamp'), dataIndex: 'timestamp', width: 180, render: (v) => dayjs(v).format('YYYY-MM-DD HH:mm:ss') },
  ]; return <Card className="panel-card no-padding"><Table rowKey="id" columns={columns} dataSource={app.events} pagination={false} scroll={{ x: 1050 }} /></Card>;
}

function YamlTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const { message } = App.useApp(); const copy = async () => { await navigator.clipboard.writeText(app.yaml); message.success(t('copied')); };
  return <Card className="panel-card code-card" title={`${app.namespace}/${app.name}`} extra={<Space><Button icon={<CopyOutlined />} onClick={copy}>{t('copy')}</Button><Button icon={<DownloadOutlined />} onClick={() => { const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([app.yaml], { type: 'text/yaml' })); a.download = `${app.name}.yaml`; a.click(); URL.revokeObjectURL(a.href); }}>Download</Button></Space>}><pre>{app.yaml}</pre></Card>;
}

export function ApplicationDetailPage() {
  const { namespace = '', name = '' } = useParams(); const { t } = useI18n(); const { session } = useAuth(); const [app, setApp] = useState<SparkApplication>(); const [error, setError] = useState(''); const [killOpen, setKillOpen] = useState(false);
  const load = useCallback(async () => { setError(''); setApp(undefined); try { setApp(await sparkService.getApplication(namespace, name)); } catch (e) { setError(e instanceof Error ? e.message : 'Failed to load'); } }, [namespace, name]);
  useEffect(() => { void load(); }, [load]);
  if (error) return <Alert type="error" showIcon message={error} action={<Button onClick={load}>{t('retry')}</Button>} />;
  if (!app) return <Skeleton active paragraph={{ rows: 10 }} />;
  const active = ['RUNNING', 'PENDING', 'SUBMITTED', 'FAILING'].includes(app.state);
  return <>
    <PageHeader back={{ label: t('applications'), to: '/applications' }} title={<Space wrap>{app.name}<StatusTag state={app.state} /></Space>} subtitle={`${app.namespace} · ${app.cluster} · ${formatDuration(app)}`} extra={<Space><Button icon={<ReloadOutlined />} onClick={load}>{t('refresh')}</Button>{runtimeConfig.features.sparkUi && <Button icon={<LinkOutlined />}>Spark UI</Button>}{runtimeConfig.features.kill && active && <Button danger icon={<DeleteOutlined />} disabled={!canKill(session?.role ?? 'viewer')} onClick={() => setKillOpen(true)}>{t('kill')}</Button>}</Space>} />
    {app.errorMessage && <Alert className="detail-alert" type="error" showIcon icon={<ExclamationCircleOutlined />} message="Application failure detected" description={app.errorMessage} />}
    <Row gutter={[16, 16]} className="detail-overview"><Col xs={24} xl={16}><Card className="panel-card"><Descriptions column={{ xs: 1, sm: 2, lg: 3 }} items={[
      { key: 'owner', label: t('owner'), children: app.owner }, { key: 'team', label: t('team'), children: app.team }, { key: 'started', label: t('startedAt'), children: app.startedAt ? dayjs(app.startedAt).format('YYYY-MM-DD HH:mm:ss') : '—' },
      { key: 'spark', label: t('sparkVersion'), children: app.sparkVersion }, { key: 'submission', label: t('submissionId'), children: <Typography.Text copyable>{app.submissionId}</Typography.Text> }, { key: 'id', label: 'Spark ID', children: app.sparkApplicationId ?? '—' },
      { key: 'image', label: t('image'), span: 3, children: <Typography.Text copyable>{app.image}</Typography.Text> },
    ]} /></Card></Col><Col xs={24} xl={8}><Card className="panel-card mini-resources"><div><span>Driver</span><b>{formatCpu(app.driver.request.cpu)} · {formatMemory(app.driver.request.memoryGiB)}</b></div><div><span>Executors</span><b>{app.executors.length} pods</b></div><div><span>Node pools</span><b>{new Set(app.executors.map((e) => e.nodePool).filter(Boolean)).size} pools</b></div></Card></Col></Row>
    <Tabs className="detail-tabs" defaultActiveKey="resources" items={[
      { key: 'resources', label: t('resources'), children: <ResourcesTab app={app} /> }, { key: 'executors', label: `${t('executors')} (${app.executors.length})`, children: <ExecutorsTab app={app} /> },
      { key: 'scheduling', label: t('scheduling'), children: <SchedulingTab app={app} /> }, { key: 'logs', label: t('logs'), children: <LogsTab app={app} /> },
      { key: 'events', label: `${t('events')} (${app.events.length})`, children: <EventsTab app={app} /> }, { key: 'yaml', label: t('yaml'), children: <YamlTab app={app} /> },
    ]} />
    <KillModal application={app} open={killOpen} onClose={() => setKillOpen(false)} onKilled={load} />
  </>;
}
