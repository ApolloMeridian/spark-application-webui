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
    resetFilters: '重置筛选', search: '搜索作业或 Owner', filter: '筛选', all: '全部', refresh: '刷新',
    overviewTab: '概览', resources: '资源', logs: 'Driver 日志', events: '事件', yaml: 'YAML',
    request: '申请', current: '当前', peak: '峰值', node: '节点', nodePool: '节点池', createdAt: '创建时间', startedAt: '开始时间', finishedAt: '完成时间',
    restarts: '重启', image: '镜像', sparkVersion: 'Spark 版本', submissionId: 'Submission ID',
    killConfirm: '确认强制终止 Spark 作业？', killWarning: '后端会先将重启策略调整为 Never，再通过运行期限终止并保留 Driver Pod，以便继续查看事件和日志；Executor Pod 会被清理，SparkApplication CR 保留到手动删除记录。',
    deleteConfirm: '确认删除 Spark 作业记录？', deleteWarning: '此操作仅允许终态作业，将从 Kubernetes 删除 SparkApplication CR；历史统计和操作审计仍保留。',
    reason: '操作原因（可选）', cancel: '取消', confirmKill: '确认终止', confirmDelete: '确认删除', noPermission: '当前角色没有终止作业的权限', noDeletePermission: '仅管理员可以删除终态作业',
    auditOperator: '操作人', operation: '操作', result: '结果', timestamp: '时间', message: '消息',
    language: '语言', role: '角色', loginTitle: '登录 Spark Control Center', loginHint: '请输入你的本地账号和密码', oidcLoginHint: '使用企业身份提供商登录；首次登录将自动创建控制台用户。',
    username: '用户名', password: '密码', login: '登录控制台', loginFailed: '登录失败', oidcLoginFailed: 'OIDC 登录失败，请重试或联系管理员', loginWithProvider: '使用 {provider} 登录', or: '或', authSource: '认证来源', localAccount: '本地账号', localLoginFooter: 'PostgreSQL 用户认证 · HttpOnly 安全会话', mockLoginHint: '使用模拟身份进入前端演示环境', mocked: 'Mock 环境', empty: '暂无数据', retry: '重试',
    copy: '复制', copied: '已复制', killSuccess: '作业强制终止请求已提交', deleteSuccess: '终态作业删除请求已提交', historicalSubmitted: '历史提交', historicalFailed: '历史失败', historyRange: '统计时间范围',
    yamlEditor: 'SparkApplication YAML 编辑器', yamlHint: '填写完整的 SparkApplication YAML，并通过后端 ServiceAccount 提交到所选命名空间。', loadExample: '加载示例', submitToCluster: '提交到集群', submissionSuccess: 'SparkApplication 已提交', submitNoPermission: '当前角色没有提交作业的权限',
    sparkUiLive: '在控制台内打开运行中的 Driver UI', sparkUiUnavailable: '当前作业没有可用的 Driver UI Service', sparkUiProxyHint: '此页面通过后端代理访问集群内 Driver UI Service。', sparkHistory: '在 Spark History Server 中查看作业', sparkHistoryUnavailable: '未启用 History Server、缺少 Spark ID，或作业未配置 EventLog',
    executorLogs: 'Executor 日志', viewExecutorLogs: '查看持久化 Executor 日志', oldestFirst: '正序', newestFirst: '倒序', queryLogs: '查询日志', lokiLogHint: '日志来自 Loki；即使 Executor Pod 已清理，仍可按作业运行时间查询持久化日志。',
    historicalUsage: '历史资源用量', metricsRange: '指标时间范围', queryMetrics: '查询指标', metricsLoadFailed: '历史指标加载失败', noMetrics: '所选时间范围内没有时序指标',
    userManagement: '用户管理', userManagementHint: '创建管理员或只读用户，并管理账号状态和密码。', createUser: '创建用户', editUser: '编辑用户', deleteUser: '删除用户', deleteUserConfirm: '确认删除这个用户？',
    displayName: '显示名称', email: '邮箱', accountStatus: '账号状态', active: '启用', disabled: '禁用', currentUser: '当前用户', viewerRole: '普通用户（只读）', adminRole: '管理员', resetPasswordOptional: '重置密码（留空则不修改）', save: '保存',
    userCreated: '用户已创建', userUpdated: '用户信息已更新', userDeleted: '用户已删除', myProfile: '个人信息', profileHint: '管理你的显示名称、邮箱和登录密码。', accountInformation: '账号信息', profileUpdated: '个人信息已更新', changePassword: '修改密码', currentPassword: '当前密码', newPassword: '新密码', confirmPassword: '确认新密码', passwordMismatch: '两次输入的密码不一致', passwordChangedLoginAgain: '密码已修改，请重新登录',
  },
  'en-US': {
    overview: 'Overview', applications: 'Spark Applications', submitApplication: 'Submit Spark Application', audit: 'Operation Audit', signOut: 'Sign out',
    cluster: 'Cluster', namespace: 'Namespace', allNamespaces: 'All namespaces', running: 'Running',
    completed: 'Completed', failed: 'Failed', pending: 'Pending', requestedCpu: 'CPU requested', usedCpu: 'CPU used',
    requestedMemory: 'Memory requested', usedMemory: 'Memory used', recentFailures: 'Recent failures', scheduling: 'Scheduling',
    application: 'Application', owner: 'Owner', status: 'Status', duration: 'Duration', driver: 'Driver',
    executors: 'Executors', cpu: 'CPU', memory: 'Memory', action: 'Action', details: 'Details', kill: 'Kill application', delete: 'Delete record',
    resetFilters: 'Reset filters', search: 'Search application or owner', filter: 'Filter', all: 'All', refresh: 'Refresh',
    overviewTab: 'Overview', resources: 'Resources', logs: 'Driver logs', events: 'Events', yaml: 'YAML',
    request: 'Request', current: 'Current', peak: 'Peak', node: 'Node', nodePool: 'Node pool', createdAt: 'Created', startedAt: 'Started', finishedAt: 'Finished',
    restarts: 'Restarts', image: 'Image', sparkVersion: 'Spark version', submissionId: 'Submission ID',
    killConfirm: 'Force terminate Spark application?', killWarning: 'The backend changes the restart policy to Never, terminates but retains the Driver Pod for events and logs, cleans Executor Pods, and keeps the SparkApplication CR until manual record deletion.',
    deleteConfirm: 'Delete terminal Spark application?', deleteWarning: 'This removes the terminal SparkApplication CR from Kubernetes. Historical statistics and operation audit remain available.',
    reason: 'Reason (optional)', cancel: 'Cancel', confirmKill: 'Kill application', confirmDelete: 'Delete application', noPermission: 'Your role cannot kill applications', noDeletePermission: 'Only administrators can delete terminal applications',
    auditOperator: 'Operator', operation: 'Operation', result: 'Result', timestamp: 'Time', message: 'Message',
    language: 'Language', role: 'Role', loginTitle: 'Sign in to Spark Control Center', loginHint: 'Enter your local username and password', oidcLoginHint: 'Sign in with your identity provider. A console user is created automatically on first sign-in.',
    username: 'Username', password: 'Password', login: 'Sign in', loginFailed: 'Login failed', oidcLoginFailed: 'OIDC sign-in failed. Try again or contact an administrator.', loginWithProvider: 'Sign in with {provider}', or: 'or', authSource: 'Authentication', localAccount: 'Local account', localLoginFooter: 'PostgreSQL authentication · Secure HttpOnly session', mockLoginHint: 'Use a simulated identity in the frontend demo', mocked: 'Mock environment', empty: 'No data', retry: 'Retry',
    copy: 'Copy', copied: 'Copied', killSuccess: 'Force termination submitted', deleteSuccess: 'Delete request submitted', historicalSubmitted: 'Historical submissions', historicalFailed: 'Historical failures', historyRange: 'Statistics range',
    yamlEditor: 'SparkApplication YAML editor', yamlHint: 'Enter a complete SparkApplication manifest and submit it with the backend ServiceAccount.', loadExample: 'Load example', submitToCluster: 'Submit to cluster', submissionSuccess: 'SparkApplication submitted', submitNoPermission: 'Your role cannot submit applications',
    sparkUiLive: 'Open the live Driver UI in the console', sparkUiUnavailable: 'No Driver UI Service is available for this application', sparkUiProxyHint: 'This page reaches the in-cluster Driver UI Service through the backend proxy.', sparkHistory: 'Open this application in Spark History Server', sparkHistoryUnavailable: 'History Server is disabled, Spark ID is missing, or EventLog is not enabled',
    executorLogs: 'Executor logs', viewExecutorLogs: 'View persisted Executor logs', oldestFirst: 'Oldest first', newestFirst: 'Newest first', queryLogs: 'Query logs', lokiLogHint: 'Logs come from Loki and remain queryable after an Executor Pod has been removed.',
    historicalUsage: 'Historical usage', metricsRange: 'Metrics time range', queryMetrics: 'Query metrics', metricsLoadFailed: 'Failed to load historical metrics', noMetrics: 'No time-series metrics in the selected range',
    userManagement: 'User management', userManagementHint: 'Create administrators or read-only users and manage account status and passwords.', createUser: 'Create user', editUser: 'Edit user', deleteUser: 'Delete user', deleteUserConfirm: 'Delete this user?',
    displayName: 'Display name', email: 'Email', accountStatus: 'Account status', active: 'Active', disabled: 'Disabled', currentUser: 'Current user', viewerRole: 'Viewer (read only)', adminRole: 'Administrator', resetPasswordOptional: 'Reset password (leave blank to keep)', save: 'Save',
    userCreated: 'User created', userUpdated: 'User updated', userDeleted: 'User deleted', myProfile: 'My profile', profileHint: 'Manage your display name, email, and login password.', accountInformation: 'Account information', profileUpdated: 'Profile updated', changePassword: 'Change password', currentPassword: 'Current password', newPassword: 'New password', confirmPassword: 'Confirm new password', passwordMismatch: 'Passwords do not match', passwordChangedLoginAgain: 'Password changed. Please sign in again.',
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
