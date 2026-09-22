import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, DatePicker, Drawer, Empty, Segmented, Space, Spin, Tag, Typography } from 'antd';
import type { Dayjs } from 'dayjs';
import { useCallback, useEffect, useState } from 'react';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import type { ExecutorPod, LogEntry, SparkApplication } from '../types';
import { displayNow, displayTime, formatTimestamp } from '../utils';

const { RangePicker } = DatePicker;

export function ExecutorLogsDrawer({ application, executor, onClose }: { application: SparkApplication; executor: ExecutorPod; onClose: () => void }) {
  const { t } = useI18n();
  const initialStart = displayTime(executor.startedAt ?? application.startedAt ?? application.createdAt).subtract(5, 'minute');
  const initialEnd = application.finishedAt ? displayTime(application.finishedAt).add(5, 'minute') : displayNow();
  const [range, setRange] = useState<[Dayjs, Dayjs]>([initialStart, initialEnd]);
  const [direction, setDirection] = useState<'forward' | 'backward'>('forward');
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const load = useCallback(async (nextDirection = direction) => {
    setLoading(true); setError('');
    try {
      setEntries(await sparkService.getExecutorLogs(application.namespace, application.name, executor.name, range[0].toISOString(), range[1].toISOString(), nextDirection));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Failed to load executor logs');
    } finally {
      setLoading(false);
    }
  }, [application.name, application.namespace, direction, executor.name, range]);
  useEffect(() => { void load(); }, []); // Each executor opens a keyed drawer instance with its own initial range.

  return <Drawer title={<Space><span>{t('executorLogs')}</span><Tag>{executor.name}</Tag></Space>} open width="min(960px, 92vw)" onClose={onClose} destroyOnClose>
    <div className="executor-log-toolbar">
      <RangePicker showTime value={range} onChange={(value) => { if (value?.[0] && value[1]) setRange([value[0], value[1]]); }} />
      <Segmented value={direction} options={[{ value: 'forward', label: t('oldestFirst') }, { value: 'backward', label: t('newestFirst') }]} onChange={(value) => { const next = value as 'forward' | 'backward'; setDirection(next); void load(next); }} />
      <Button icon={<ReloadOutlined />} onClick={() => void load()}>{t('queryLogs')}</Button>
    </div>
    <Typography.Paragraph type="secondary">{t('lokiLogHint')}</Typography.Paragraph>
    {error && <Alert className="detail-alert" type="error" showIcon message={error} action={<Button onClick={() => void load()}>{t('retry')}</Button>} />}
    {loading ? <div className="drawer-loading"><Spin /></div> : entries.length ? <pre className="log-viewer executor-log-viewer">{entries.map((entry, index) => <div key={`${entry.timestamp}-${index}`}><span>{formatTimestamp(entry.timestamp, 'YYYY-MM-DD HH:mm:ss.SSS')}</span>{entry.line}</div>)}</pre> : <Empty description={t('empty')} />}
  </Drawer>;
}
