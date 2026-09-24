import { BranchesOutlined, CopyOutlined, DeleteOutlined, DownloadOutlined, ExclamationCircleOutlined, FileTextOutlined, LinkOutlined, RedoOutlined, ReloadOutlined, SearchOutlined, StopOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Col, DatePicker, Descriptions, Empty, Input, Progress, Row, Segmented, Skeleton, Space, Spin, Table, Tabs, Tag, Timeline, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import ReactECharts from 'echarts-for-react';
import type { Dayjs } from 'dayjs';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useAuth } from '../auth';
import { ApplicationOperationModal } from '../components/KillModal';
import { ExecutorLogsDrawer } from '../components/ExecutorLogsDrawer';
import { PageHeader } from '../components/PageHeader';
import { StatusTag } from '../components/StatusTag';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import { runtimeConfig } from '../runtimeConfig';
import type { ExecutorPod, KubernetesEvent, SparkApplication } from '../types';
import { canDelete, canKill, canSubmit, displayNow, displayTime, formatCpu, formatDuration, formatMemory, formatTimestamp, isKillableState, isTerminalState, sumResources } from '../utils';
import { useApplicationEvents } from '../useApplicationEvents';

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
  const { t } = useI18n();
  const [metric, setMetric] = useState<'cpu' | 'memoryGiB'>('cpu');
  const [range, setRange] = useState<[Dayjs, Dayjs]>(() => {
    if (app.metrics.length) {
      return [displayTime(app.metrics[0].time).subtract(1, 'minute'), displayTime(app.metrics[app.metrics.length - 1].time).add(1, 'minute')];
    }
    const end = app.finishedAt ? displayTime(app.finishedAt).add(5, 'minute') : displayNow();
    const lifecycleStart = displayTime(app.startedAt ?? app.createdAt).subtract(5, 'minute');
    const oneHourStart = end.subtract(1, 'hour');
    return [lifecycleStart.isAfter(oneHourStart) ? lifecycleStart : oneHourStart, end];
  });
  const [points, setPoints] = useState(app.metrics);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const load = useCallback(async () => {
    setLoading(true); setError('');
    try { setPoints(await sparkService.getApplicationMetrics(app.namespace, app.name, range[0].toISOString(), range[1].toISOString())); }
    catch (caught) { setError(caught instanceof Error ? caught.message : t('metricsLoadFailed')); }
    finally { setLoading(false); }
  }, [app.name, app.namespace, range, t]);
  useEffect(() => { void load(); }, []); // The keyed application page supplies the initial job-aware time window.
  const option = useMemo(() => ({
    animation: false,
    tooltip: { trigger: 'axis' }, grid: { top: 28, left: 52, right: 24, bottom: 42 },
    xAxis: { type: 'category', boundaryGap: false, data: points.map((p) => formatTimestamp(p.time, 'MM-DD HH:mm:ss')), axisLine: { lineStyle: { color: '#dce2eb' } }, axisLabel: { color: '#7d8799' } },
    yAxis: { type: 'value', name: metric === 'cpu' ? 'cores' : 'GiB', splitLine: { lineStyle: { color: '#edf1f6' } } },
    series: [{ name: metric === 'cpu' ? 'CPU used' : 'Memory used', type: 'line', smooth: true, showSymbol: false, data: points.map((p) => p[metric]), lineStyle: { width: 3, color: metric === 'cpu' ? '#2868f0' : '#7a58e8' }, areaStyle: { color: metric === 'cpu' ? 'rgba(40,104,240,.11)' : 'rgba(122,88,232,.11)' } }],
  }), [metric, points]);
  const lifecycleEnd = app.finishedAt ? displayTime(app.finishedAt).add(5, 'minute') : displayNow();
  const lifecycleStart = displayTime(app.startedAt ?? app.createdAt).subtract(5, 'minute');
  return <div className="tab-stack"><ResourceSummary app={app} /><Card className="panel-card" title={t('historicalUsage')}><div className="metrics-toolbar"><DatePicker.RangePicker aria-label={t('metricsRange')} showTime allowClear={false} value={range} presets={[{ label: '15m', value: [displayNow().subtract(15, 'minute'), displayNow()] }, { label: '1h', value: [displayNow().subtract(1, 'hour'), displayNow()] }, { label: '6h', value: [displayNow().subtract(6, 'hour'), displayNow()] }, { label: '24h', value: [displayNow().subtract(24, 'hour'), displayNow()] }, { label: t('duration'), value: [lifecycleStart, lifecycleEnd] }]} onChange={(value) => { if (value?.[0] && value[1]) setRange([value[0], value[1]]); }} /><Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load()}>{t('queryMetrics')}</Button><Segmented value={metric} onChange={(value) => setMetric(value as 'cpu' | 'memoryGiB')} options={[{ value: 'cpu', label: 'CPU' }, { value: 'memoryGiB', label: 'Memory' }]} /></div>{error && <Alert className="detail-alert" type="error" showIcon message={error} />}{loading ? <div className="drawer-loading"><Spin /></div> : points.length ? <ReactECharts option={option} style={{ height: 310 }} /> : <Empty description={t('noMetrics')} />}</Card></div>;
}

function ExecutorsTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const [logTarget, setLogTarget] = useState<ExecutorPod>();
  const columns: ColumnsType<ExecutorPod> = [
    { title: 'Pod', dataIndex: 'name', width: 260, render: (value) => <Typography.Text copyable={{ text: value }}>{value}</Typography.Text> },
    { title: t('status'), dataIndex: 'state', width: 125, render: (value, row) => <Tooltip title={row.rawState && row.rawState !== value ? `Spark Operator raw state: ${row.rawState}` : undefined}><Tag color={value === 'RUNNING' ? 'success' : value === 'PENDING' ? 'warning' : value === 'FAILED' ? 'error' : value === 'TERMINATED' ? 'default' : 'blue'}>{value}</Tag></Tooltip> },
    { title: t('cpu'), width: 180, render: (_, row) => <div className="table-progress"><span>{formatCpu(row.resources.current?.cpu)} / {formatCpu(row.resources.request.cpu)}</span><Progress size="small" percent={Math.round((row.resources.current?.cpu ?? 0) / row.resources.request.cpu * 100)} showInfo={false} /></div> },
    { title: t('memory'), width: 190, render: (_, row) => <div className="table-progress"><span>{formatMemory(row.resources.current?.memoryGiB)} / {formatMemory(row.resources.request.memoryGiB)}</span><Progress size="small" percent={Math.round((row.resources.current?.memoryGiB ?? 0) / row.resources.request.memoryGiB * 100)} showInfo={false} strokeColor="#7a58e8" /></div> },
    { title: t('node'), dataIndex: 'node', width: 165, render: (value) => value ?? <Typography.Text type="warning">Unscheduled</Typography.Text> },
    { title: t('nodePool'), dataIndex: 'nodePool', width: 110, render: (value) => value ? <Tag color={value === 'autoscale' ? 'green' : 'blue'}>{value}</Tag> : '—' },
    { title: t('restarts'), dataIndex: 'restarts', align: 'center', width: 90 },
    ...(runtimeConfig.features.executorLogs ? [{ title: t('executorLogs'), key: 'logs', fixed: 'right' as const, width: 105, align: 'center' as const, render: (_: unknown, row: ExecutorPod) => <Tooltip title={t('viewExecutorLogs')}><Button aria-label={t('viewExecutorLogs')} type="text" size="small" icon={<FileTextOutlined />} onClick={() => setLogTarget(row)} /></Tooltip> }] : []),
  ];
  return <><Card className="panel-card no-padding"><Table rowKey="name" columns={columns} dataSource={app.executors} pagination={false} scroll={{ x: 1240 }} /></Card>{logTarget && <ExecutorLogsDrawer key={logTarget.name} application={app} executor={logTarget} onClose={() => setLogTarget(undefined)} />}</>;
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
    { title: t('message'), dataIndex: 'message' }, { title: 'Source', dataIndex: 'source', width: 180 }, { title: 'Count', dataIndex: 'count', width: 80, align: 'center' }, { title: t('timestamp'), dataIndex: 'timestamp', width: 180, render: (v) => formatTimestamp(v) },
  ]; return <Card className="panel-card no-padding"><Table rowKey="id" columns={columns} dataSource={app.events} pagination={false} scroll={{ x: 1050 }} /></Card>;
}

function YamlTab({ app }: { app: SparkApplication }) {
  const { t } = useI18n(); const { message } = App.useApp(); const copy = async () => { await navigator.clipboard.writeText(app.yaml); message.success(t('copied')); };
  return <Card className="panel-card code-card" title={`${app.namespace}/${app.name}`} extra={<Space><Button icon={<CopyOutlined />} onClick={copy}>{t('copy')}</Button><Button icon={<DownloadOutlined />} onClick={() => { const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([app.yaml], { type: 'text/yaml' })); a.download = `${app.name}.yaml`; a.click(); URL.revokeObjectURL(a.href); }}>Download</Button></Space>}><pre>{app.yaml}</pre></Card>;
}

export function ApplicationDetailPage() {
  const { namespace = '', name = '' } = useParams(); const navigate = useNavigate(); const { t } = useI18n(); const { session } = useAuth(); const [app, setApp] = useState<SparkApplication>(); const [error, setError] = useState(''); const [actionError, setActionError] = useState(''); const [operation, setOperation] = useState<'kill' | 'delete'>(); const [preparing, setPreparing] = useState<'clone' | 'retry'>();
  const load = useCallback(async () => { setError(''); try { setApp(await sparkService.getApplication(namespace, name)); } catch (e) { setError(e instanceof Error ? e.message : 'Failed to load'); } }, [namespace, name]);
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), runtimeConfig.dashboard.refreshIntervalSeconds * 1000); return () => window.clearInterval(timer); }, [load]);
  useApplicationEvents((change) => { if (change.namespace === namespace && change.name === name) void load(); });
  if (error) return <Alert type="error" showIcon message={error} action={<Button onClick={load}>{t('retry')}</Button>} />;
  if (!app) return <Skeleton active paragraph={{ rows: 10 }} />;
  const historyAvailable = runtimeConfig.historyServer.enabled && !!runtimeConfig.historyServer.baseUrl && !!app.eventLogEnabled && !!app.sparkApplicationId && isTerminalState(app.state);
  const openSparkUI = () => {
    if (app.state === 'RUNNING' && app.sparkUiAvailable) {
      navigate(`/applications/${encodeURIComponent(app.namespace)}/${encodeURIComponent(app.name)}/spark-ui`);
      return;
    }
    if (historyAvailable) {
      window.open(`${runtimeConfig.historyServer.baseUrl}/${encodeURIComponent(app.sparkApplicationId!)}/jobs/`, '_blank', 'noopener,noreferrer');
    }
  };
  const sparkUITooltip = app.state === 'RUNNING' ? (app.sparkUiAvailable ? t('sparkUiLive') : t('sparkUiUnavailable')) : (historyAvailable ? t('sparkHistory') : t('sparkHistoryUnavailable'));
  const prepare = async (mode: 'clone' | 'retry') => {
    setPreparing(mode); setActionError('');
    try {
      const result = await sparkService.prepareApplication(app.namespace, app.name, mode);
      navigate('/submit', { state: { manifest: result.serverYaml, namespace: result.namespace, source: `${app.namespace}/${app.name}`, mode } });
    } catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Failed to prepare manifest'); }
    finally { setPreparing(undefined); }
  };
  return <>
    <PageHeader back={{ label: t('applications'), to: '/applications' }} title={<Space wrap>{app.name}<StatusTag state={app.state} />{app.historical && <Tag color="purple">{t('historicalSnapshot')}</Tag>}</Space>} subtitle={`${app.namespace} · ${app.cluster} · ${formatDuration(app)}`} extra={<Space wrap><Button icon={<ReloadOutlined />} onClick={load}>{t('refresh')}</Button>{runtimeConfig.features.sparkUi && <Tooltip title={sparkUITooltip}><Button icon={<LinkOutlined />} disabled={app.state === 'RUNNING' ? !app.sparkUiAvailable : !historyAvailable} onClick={openSparkUI}>{app.state === 'RUNNING' ? 'Spark UI' : 'Spark History'}</Button></Tooltip>}{runtimeConfig.features.submit && canSubmit(session?.role ?? 'viewer') && <Button icon={<BranchesOutlined />} loading={preparing === 'clone'} onClick={() => void prepare('clone')}>{t('cloneApplication')}</Button>}{runtimeConfig.features.submit && canSubmit(session?.role ?? 'viewer') && isTerminalState(app.state) && <Button icon={<RedoOutlined />} loading={preparing === 'retry'} onClick={() => void prepare('retry')}>{t('retryApplication')}</Button>}{runtimeConfig.features.kill && isKillableState(app.state) && !app.historical && <Button danger icon={<StopOutlined />} disabled={!canKill(session?.role ?? 'viewer')} onClick={() => setOperation('kill')}>{t('kill')}</Button>}{runtimeConfig.features.kill && isTerminalState(app.state) && !app.historical && <Button danger icon={<DeleteOutlined />} disabled={!canDelete(session?.role ?? 'viewer')} onClick={() => setOperation('delete')}>{t('delete')}</Button>}</Space>} />
    {actionError && <Alert className="detail-alert" type="error" showIcon closable onClose={() => setActionError('')} message={actionError} />}
    {app.errorMessage && <Alert className="detail-alert" type="error" showIcon icon={<ExclamationCircleOutlined />} message="Application failure detected" description={app.errorMessage} />}
    {!!app.diagnostics?.length && <Card className="panel-card" title={t('diagnosis')}><div className="diagnostic-list">{app.diagnostics.map((item) => <div key={item.code} className={`diagnostic-card ${item.severity}`}><Space><Tag color={item.severity === 'error' ? 'red' : 'orange'}>{item.code}</Tag><Typography.Text strong>{item.summary}</Typography.Text></Space>{item.detail && <small>{item.detail}</small>}{item.recommendation && <small><b>{t('recommendation')}:</b> {item.recommendation}</small>}</div>)}</div></Card>}
    <Row gutter={[16, 16]} className="detail-overview"><Col xs={24} xl={16}><Card className="panel-card"><Descriptions column={{ xs: 1, sm: 2, lg: 3 }} items={[
      { key: 'owner', label: t('owner'), children: app.owner }, { key: 'created', label: t('createdAt'), children: formatTimestamp(app.createdAt) }, { key: 'started', label: t('startedAt'), children: formatTimestamp(app.startedAt) },
      { key: 'finished', label: t('finishedAt'), children: formatTimestamp(app.finishedAt) }, { key: 'spark', label: t('sparkVersion'), children: app.sparkVersion }, { key: 'submission', label: t('submissionId'), children: <Typography.Text copyable>{app.submissionId}</Typography.Text> },
      { key: 'id', label: 'Spark ID', children: app.sparkApplicationId ?? '—' }, { key: 'driverPod', label: 'Driver Pod', children: <Typography.Text copyable>{app.driverPod}</Typography.Text> }, { key: 'namespace', label: t('namespace'), children: app.namespace },
      { key: 'image', label: t('image'), span: 3, children: <Typography.Text copyable>{app.image}</Typography.Text> },
    ]} /></Card></Col><Col xs={24} xl={8}><Card className="panel-card mini-resources"><div><span>Driver</span><b>{formatCpu(app.driver.request.cpu)} · {formatMemory(app.driver.request.memoryGiB)}</b></div><div><span>Executors</span><b>{app.executors.length} pods</b></div><div><span>Node pools</span><b>{new Set(app.executors.map((e) => e.nodePool).filter(Boolean)).size} pools</b></div></Card></Col></Row>
    <Tabs className="detail-tabs" defaultActiveKey="resources" items={[
      { key: 'resources', label: t('resources'), children: <ResourcesTab app={app} /> }, { key: 'executors', label: `${t('executors')} (${app.executors.length})`, children: <ExecutorsTab app={app} /> },
      { key: 'scheduling', label: t('scheduling'), children: <SchedulingTab app={app} /> }, { key: 'logs', label: t('logs'), children: <LogsTab app={app} /> },
      { key: 'lifecycle', label: `${t('lifecycle')} (${app.lifecycle?.length ?? 0})`, children: <Card className="panel-card"><Timeline items={(app.lifecycle ?? []).map((item) => ({ color: item.type === 'warning' ? 'red' : item.type === 'finished' ? 'green' : 'blue', children: <div><Typography.Text strong>{item.title}</Typography.Text><Typography.Text type="secondary"> · {formatTimestamp(item.time)}</Typography.Text>{item.detail && <Typography.Paragraph type="secondary">{item.detail}</Typography.Paragraph>}</div> }))} /></Card> },
      { key: 'events', label: `${t('events')} (${app.events.length})`, children: <EventsTab app={app} /> }, { key: 'yaml', label: t('yaml'), children: <YamlTab app={app} /> },
    ]} />
    {operation && <ApplicationOperationModal application={app} operation={operation} open onClose={() => setOperation(undefined)} onCompleted={() => { if (operation === 'delete') navigate('/applications'); else void load(); }} />}
  </>;
}
