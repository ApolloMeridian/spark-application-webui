import { createContext, useContext, useMemo, useState, type ReactNode } from 'react';
import type { Locale } from './types';

const messages = {
  'zh-CN': {
    overview: '运行总览', applications: 'Spark 作业', submitApplication: '提交 Spark 作业', audit: '操作审计', signOut: '退出登录',
    cluster: '集群', namespace: '命名空间', allNamespaces: '全部命名空间', running: '运行中',
    completed: '已完成', failed: '失败', pending: '等待中', requestedCpu: 'CPU 申请', usedCpu: 'CPU 使用',
    requestedMemory: '内存申请', usedMemory: '内存使用', recentFailures: '近期失败', scheduling: '调度分布',
    application: '作业名称', owner: 'Owner', status: '状态', duration: '运行时长', driver: 'Driver',
    executors: 'Executors', cpu: 'CPU', memory: '内存', action: '操作', details: '查看详情', kill: '终止作业', delete: '删除记录',
    resetFilters: '重置筛选', search: '搜索作业、Owner 或 Team', filter: '筛选', all: '全部', refresh: '刷新',
    overviewTab: '概览', resources: '资源', logs: 'Driver 日志', events: '事件', yaml: 'YAML',
    request: '申请', current: '当前', peak: '峰值', node: '节点', nodePool: '节点池', startedAt: '开始时间',
    restarts: '重启', image: '镜像', sparkVersion: 'Spark 版本', submissionId: 'Submission ID', team: '团队',
    killConfirm: '确认强制终止 Spark 作业？', killWarning: '后端会先将重启策略调整为 Never，再以零宽限期强制删除 Driver 和 Executor Pod。SparkApplication CR 会保留，最终失败状态由 Spark Operator 更新。',
    deleteConfirm: '确认删除 Spark 作业记录？', deleteWarning: '此操作仅允许终态作业，将从 Kubernetes 删除 SparkApplication CR；历史统计和操作审计仍保留。',
    reason: '操作原因（可选）', cancel: '取消', confirmKill: '确认终止', confirmDelete: '确认删除', noPermission: '当前角色没有终止作业的权限', noDeletePermission: '仅管理员可以删除终态作业',
    auditOperator: '操作人', operation: '操作', result: '结果', timestamp: '时间', message: '消息',
    language: '语言', role: '模拟角色', loginTitle: '登录 Spark Control Center', loginHint: '使用模拟身份进入运维控制台',
    username: '用户名', login: '登录控制台', mocked: 'Mock 环境', empty: '暂无数据', retry: '重试',
    copy: '复制', copied: '已复制', killSuccess: '作业强制终止请求已提交', deleteSuccess: '终态作业删除请求已提交', historicalSubmitted: '历史提交', historicalFailed: '历史失败', historyRange: '统计时间范围',
    yamlEditor: 'SparkApplication YAML 编辑器', yamlHint: '填写完整的 SparkApplication YAML，并通过后端 ServiceAccount 提交到所选命名空间。', loadExample: '加载示例', submitToCluster: '提交到集群', submissionSuccess: 'SparkApplication 已提交', submitNoPermission: '当前角色没有提交作业的权限',
    sparkUiLive: '在控制台内打开运行中的 Driver UI', sparkUiUnavailable: '当前作业没有可用的 Driver UI Service', sparkUiProxyHint: '此页面通过后端代理访问集群内 Driver UI Service。', sparkHistory: '在 Spark History Server 中查看作业', sparkHistoryUnavailable: '未启用 History Server、缺少 Spark ID，或作业未配置 EventLog',
  },
  'en-US': {
    overview: 'Overview', applications: 'Spark Applications', submitApplication: 'Submit Spark Application', audit: 'Operation Audit', signOut: 'Sign out',
    cluster: 'Cluster', namespace: 'Namespace', allNamespaces: 'All namespaces', running: 'Running',
    completed: 'Completed', failed: 'Failed', pending: 'Pending', requestedCpu: 'CPU requested', usedCpu: 'CPU used',
    requestedMemory: 'Memory requested', usedMemory: 'Memory used', recentFailures: 'Recent failures', scheduling: 'Scheduling',
    application: 'Application', owner: 'Owner', status: 'Status', duration: 'Duration', driver: 'Driver',
    executors: 'Executors', cpu: 'CPU', memory: 'Memory', action: 'Action', details: 'Details', kill: 'Kill application', delete: 'Delete record',
    resetFilters: 'Reset filters', search: 'Search application, owner or team', filter: 'Filter', all: 'All', refresh: 'Refresh',
    overviewTab: 'Overview', resources: 'Resources', logs: 'Driver logs', events: 'Events', yaml: 'YAML',
    request: 'Request', current: 'Current', peak: 'Peak', node: 'Node', nodePool: 'Node pool', startedAt: 'Started',
    restarts: 'Restarts', image: 'Image', sparkVersion: 'Spark version', submissionId: 'Submission ID', team: 'Team',
    killConfirm: 'Force terminate Spark application?', killWarning: 'The backend changes the restart policy to Never, then force-deletes Driver and Executor pods with zero grace. The SparkApplication CR is retained and Spark Operator determines its final failed state.',
    deleteConfirm: 'Delete terminal Spark application?', deleteWarning: 'This removes the terminal SparkApplication CR from Kubernetes. Historical statistics and operation audit remain available.',
    reason: 'Reason (optional)', cancel: 'Cancel', confirmKill: 'Kill application', confirmDelete: 'Delete application', noPermission: 'Your role cannot kill applications', noDeletePermission: 'Only administrators can delete terminal applications',
    auditOperator: 'Operator', operation: 'Operation', result: 'Result', timestamp: 'Time', message: 'Message',
    language: 'Language', role: 'Demo role', loginTitle: 'Sign in to Spark Control Center', loginHint: 'Use a simulated identity to enter the console',
    username: 'Username', login: 'Open console', mocked: 'Mock environment', empty: 'No data', retry: 'Retry',
    copy: 'Copy', copied: 'Copied', killSuccess: 'Force termination submitted', deleteSuccess: 'Delete request submitted', historicalSubmitted: 'Historical submissions', historicalFailed: 'Historical failures', historyRange: 'Statistics range',
    yamlEditor: 'SparkApplication YAML editor', yamlHint: 'Enter a complete SparkApplication manifest and submit it with the backend ServiceAccount.', loadExample: 'Load example', submitToCluster: 'Submit to cluster', submissionSuccess: 'SparkApplication submitted', submitNoPermission: 'Your role cannot submit applications',
    sparkUiLive: 'Open the live Driver UI in the console', sparkUiUnavailable: 'No Driver UI Service is available for this application', sparkUiProxyHint: 'This page reaches the in-cluster Driver UI Service through the backend proxy.', sparkHistory: 'Open this application in Spark History Server', sparkHistoryUnavailable: 'History Server is disabled, Spark ID is missing, or EventLog is not enabled',
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
