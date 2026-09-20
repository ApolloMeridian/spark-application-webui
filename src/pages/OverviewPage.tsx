import { ArrowRightOutlined, ClockCircleOutlined, ExclamationCircleOutlined, HistoryOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, DatePicker, Progress, Row, Skeleton, Space, Statistic, Table, Typography } from 'antd';
import ReactECharts from 'echarts-for-react';
import dayjs, { type Dayjs } from 'dayjs';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import type { DashboardSummary, SparkApplication } from '../types';
import { formatCpu, formatMemory, stateColor } from '../utils';
import { PageHeader } from '../components/PageHeader';
import { StatusTag } from '../components/StatusTag';
import { runtimeConfig } from '../runtimeConfig';

function UsageCard({ title, used, requested, capacity, formatter, accent, metricsAvailable }: { title: string; used: number; requested: number; capacity: number; formatter: (n: number) => string; accent: string; metricsAvailable: boolean }) {
  const percent = Math.round((requested / capacity) * 100);
  return <Card className="usage-card"><div className="usage-title">{title}<ThunderboltOutlined /></div><div className="usage-primary">{metricsAvailable ? formatter(used) : '—'} <small>{metricsAvailable ? 'used' : 'Prometheus unavailable'}</small></div><div className="usage-secondary"><span>{formatter(requested)} requested</span><span>{formatter(capacity)} capacity</span></div><Progress percent={percent} showInfo={false} strokeColor={accent} trailColor="#edf1f6" /></Card>;
}

export function OverviewPage() {
  const { t } = useI18n(); const navigate = useNavigate(); const [summary, setSummary] = useState<DashboardSummary>(); const [apps, setApps] = useState<SparkApplication[]>([]); const [error, setError] = useState(''); const [lastUpdated, setLastUpdated] = useState<Dayjs>();
  const [historyRange, setHistoryRange] = useState<[Dayjs, Dayjs]>(() => [dayjs().subtract(runtimeConfig.dashboard.defaultHistoryDays, 'day'), dayjs()]);
  const load = useCallback(async () => { setError(''); try { const [s, list] = await Promise.all([sparkService.getSummary({ from: historyRange[0].toISOString(), to: historyRange[1].toISOString() }), sparkService.listApplications()]); setSummary(s); setApps(list); setLastUpdated(dayjs()); } catch (e) { setError(e instanceof Error ? e.message : 'Failed to load'); } }, [historyRange]);
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), runtimeConfig.dashboard.refreshIntervalSeconds * 1000); return () => window.clearInterval(timer); }, [load]);
  if (error) return <Alert type="error" showIcon message={error} action={<Button onClick={load}>{t('retry')}</Button>} />;
  const failures = apps.filter((app) => ['FAILED', 'SUBMISSION_FAILED', 'FAILING'].includes(app.state)).slice(0, 4);
  const statusData = ['RUNNING', 'PENDING', 'FAILED', 'COMPLETED'].map((state) => ({ value: summary?.byState[state as keyof typeof summary.byState] ?? 0, name: state, itemStyle: { color: stateColor[state as keyof typeof stateColor] } }));
  const chartOption = { animation: false, tooltip: { trigger: 'item' }, legend: { bottom: 0, icon: 'circle', itemWidth: 8, textStyle: { color: '#647087' } }, series: [{ type: 'pie', radius: ['58%', '78%'], center: ['50%', '43%'], label: { show: false }, data: statusData }] };
  return <>
    <PageHeader title={t('overview')} subtitle={`${runtimeConfig.cluster.name} · ${lastUpdated ? lastUpdated.format('HH:mm:ss') : '—'}`} extra={<Space wrap><DatePicker.RangePicker aria-label={t('historyRange')} allowClear={false} value={historyRange} presets={[{ label: '24h', value: [dayjs().subtract(1, 'day'), dayjs()] }, { label: '7d', value: [dayjs().subtract(7, 'day'), dayjs()] }, { label: '30d', value: [dayjs().subtract(30, 'day'), dayjs()] }, { label: '90d', value: [dayjs().subtract(90, 'day'), dayjs()] }]} onChange={(value) => { if (value?.[0] && value[1]) setHistoryRange([value[0].startOf('day'), value[1].endOf('day')]); }} /><Button onClick={load}>{t('refresh')}</Button></Space>} />
    {!summary ? <Skeleton active /> : <>
      <Row gutter={[16, 16]} className="status-grid">
        {[
          ['RUNNING', t('running'), summary.byState.RUNNING ?? 0, '#12a878'], ['PENDING', t('pending'), (summary.byState.PENDING ?? 0) + (summary.byState.SUBMITTED ?? 0), '#e5a11a'],
          ['FAILED', t('failed'), (summary.byState.FAILED ?? 0) + (summary.byState.SUBMISSION_FAILED ?? 0), '#e44c55'], ['COMPLETED', t('completed'), summary.byState.COMPLETED ?? 0, '#2878ff'],
          ['HISTORY', t('historicalSubmitted'), summary.history.submitted, '#7a58e8'], ['HISTORY_FAILED', t('historicalFailed'), summary.history.failed, '#c9323d'],
        ].map(([key, label, value, color]) => <Col xs={12} lg={8} xl={4} key={key as string}><Card className="stat-card" onClick={() => key === 'HISTORY_FAILED' ? navigate('/applications?state=FAILED') : key !== 'HISTORY' && navigate(`/applications?state=${key}`)}><div className="stat-accent" style={{ background: color as string }} /><Statistic title={<Space size={6}>{String(key).startsWith('HISTORY') && <HistoryOutlined />}{label}</Space>} value={value as number} suffix={!String(key).startsWith('HISTORY') ? <ArrowRightOutlined /> : undefined} /></Card></Col>)}
      </Row>
      <Row gutter={[16, 16]} className="dashboard-row">
        <Col xs={24} xl={16}><Row gutter={[16, 16]}><Col xs={24} md={12}><UsageCard title={t('cpu')} used={summary.used.cpu} requested={summary.requested.cpu} capacity={summary.capacity.cpu} formatter={formatCpu} accent="#2868f0" metricsAvailable={summary.metricsAvailable} /></Col><Col xs={24} md={12}><UsageCard title={t('memory')} used={summary.used.memoryGiB} requested={summary.requested.memoryGiB} capacity={summary.capacity.memoryGiB} formatter={formatMemory} accent="#7a58e8" metricsAvailable={summary.metricsAvailable} /></Col></Row>
          <Card className="panel-card" title={<Space><ExclamationCircleOutlined className="danger-text" />{t('recentFailures')}</Space>} extra={<Button type="link" onClick={() => navigate('/applications?state=FAILED')}>{t('applications')} <ArrowRightOutlined /></Button>}>
            <Table size="small" pagination={false} rowKey="id" dataSource={failures} onRow={(record) => ({ onClick: () => navigate(`/applications/${record.namespace}/${record.name}`) })} columns={[
              { title: t('application'), dataIndex: 'name', render: (value, row) => <div><b>{value}</b><small className="cell-subtitle">{row.namespace}</small></div> },
              { title: t('status'), dataIndex: 'state', render: (state) => <StatusTag state={state} /> },
              { title: t('owner'), dataIndex: 'owner' },
              { title: 'Reason', dataIndex: 'errorMessage', ellipsis: true },
            ]} />
          </Card>
        </Col>
        <Col xs={24} xl={8}><Card className="panel-card health-card" title="Application health"><ReactECharts option={chartOption} style={{ height: 245 }} /><div className="donut-center"><b>{summary.total}</b><span>total</span></div></Card>
          <Card className="panel-card scheduling-card" title={t('scheduling')}><div className="pool-row"><span><i className="pool-dot baseline" />Baseline pool</span><b>{summary.nodePools.baseline}</b></div><Progress percent={Math.round(summary.nodePools.baseline / Math.max(1, summary.nodePools.baseline + summary.nodePools.autoscale) * 100)} showInfo={false} strokeColor="#2868f0" /><div className="pool-row"><span><i className="pool-dot autoscale" />Autoscale pool</span><b>{summary.nodePools.autoscale}</b></div><Progress percent={Math.round(summary.nodePools.autoscale / Math.max(1, summary.nodePools.baseline + summary.nodePools.autoscale) * 100)} showInfo={false} strokeColor="#12a878" />{summary.nodePools.pending > 0 && <div className="pending-callout"><ClockCircleOutlined /><span><b>{summary.nodePools.pending} pods pending</b><small>Waiting for scheduling capacity</small></span></div>}</Card>
        </Col>
      </Row>
    </>}
  </>;
}
