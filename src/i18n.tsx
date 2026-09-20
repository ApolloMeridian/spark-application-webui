import { createContext, useContext, useMemo, useState, type ReactNode } from 'react';
import type { Locale } from './types';

const messages = {
  'zh-CN': {
    overview: '运行总览', applications: 'Spark 作业', audit: '操作审计', signOut: '退出登录',
    cluster: '集群', namespace: '命名空间', allNamespaces: '全部命名空间', running: '运行中',
    completed: '已完成', failed: '失败', pending: '等待中', requestedCpu: 'CPU 申请', usedCpu: 'CPU 使用',
    requestedMemory: '内存申请', usedMemory: '内存使用', recentFailures: '近期失败', scheduling: '调度分布',
    application: '作业名称', owner: 'Owner', status: '状态', duration: '运行时长', driver: 'Driver',
    executors: 'Executors', cpu: 'CPU', memory: '内存', action: '操作', details: '查看详情', kill: '终止作业',
    reset: '重置演示数据', search: '搜索作业、Owner 或 Team', filter: '筛选', all: '全部', refresh: '刷新',
    overviewTab: '概览', resources: '资源', logs: 'Driver 日志', events: '事件', yaml: 'YAML',
    request: '申请', current: '当前', peak: '峰值', node: '节点', nodePool: '节点池', startedAt: '开始时间',
    restarts: '重启', image: '镜像', sparkVersion: 'Spark 版本', submissionId: 'Submission ID', team: '团队',
    killConfirm: '确认终止 Spark 作业？', killWarning: '此操作将删除 SparkApplication，并终止关联的 Driver 和 Executor。',
    reason: '操作原因（可选）', cancel: '取消', confirmKill: '确认终止', noPermission: '当前角色没有终止作业的权限',
    auditOperator: '操作人', operation: '操作', result: '结果', timestamp: '时间', message: '消息',
    language: '语言', role: '模拟角色', loginTitle: '登录 Spark Control Center', loginHint: '使用模拟身份进入运维控制台',
    username: '用户名', login: '登录控制台', mocked: 'Mock 环境', empty: '暂无数据', retry: '重试',
    copy: '复制', copied: '已复制', resetDone: '演示数据已重置', killSuccess: '作业终止请求已提交',
  },
  'en-US': {
    overview: 'Overview', applications: 'Spark Applications', audit: 'Operation Audit', signOut: 'Sign out',
    cluster: 'Cluster', namespace: 'Namespace', allNamespaces: 'All namespaces', running: 'Running',
    completed: 'Completed', failed: 'Failed', pending: 'Pending', requestedCpu: 'CPU requested', usedCpu: 'CPU used',
    requestedMemory: 'Memory requested', usedMemory: 'Memory used', recentFailures: 'Recent failures', scheduling: 'Scheduling',
    application: 'Application', owner: 'Owner', status: 'Status', duration: 'Duration', driver: 'Driver',
    executors: 'Executors', cpu: 'CPU', memory: 'Memory', action: 'Action', details: 'Details', kill: 'Kill application',
    reset: 'Reset demo data', search: 'Search application, owner or team', filter: 'Filter', all: 'All', refresh: 'Refresh',
    overviewTab: 'Overview', resources: 'Resources', logs: 'Driver logs', events: 'Events', yaml: 'YAML',
    request: 'Request', current: 'Current', peak: 'Peak', node: 'Node', nodePool: 'Node pool', startedAt: 'Started',
    restarts: 'Restarts', image: 'Image', sparkVersion: 'Spark version', submissionId: 'Submission ID', team: 'Team',
    killConfirm: 'Kill Spark application?', killWarning: 'This deletes the SparkApplication and terminates its Driver and Executors.',
    reason: 'Reason (optional)', cancel: 'Cancel', confirmKill: 'Kill application', noPermission: 'Your role cannot kill applications',
    auditOperator: 'Operator', operation: 'Operation', result: 'Result', timestamp: 'Time', message: 'Message',
    language: 'Language', role: 'Demo role', loginTitle: 'Sign in to Spark Control Center', loginHint: 'Use a simulated identity to enter the console',
    username: 'Username', login: 'Open console', mocked: 'Mock environment', empty: 'No data', retry: 'Retry',
    copy: 'Copy', copied: 'Copied', resetDone: 'Demo data reset', killSuccess: 'Kill request submitted',
  },
} as const;

type MessageKey = keyof typeof messages['zh-CN'];
interface I18nValue { locale: Locale; setLocale: (locale: Locale) => void; t: (key: MessageKey) => string }
const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => (localStorage.getItem('spark-console-locale') as Locale) || 'zh-CN');
  const value = useMemo(() => ({
    locale,
    setLocale: (next: Locale) => { localStorage.setItem('spark-console-locale', next); setLocaleState(next); },
    t: (key: MessageKey) => messages[locale][key],
  }), [locale]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const value = useContext(I18nContext);
  if (!value) throw new Error('useI18n must be used inside I18nProvider');
  return value;
}
