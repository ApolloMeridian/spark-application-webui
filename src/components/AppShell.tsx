import { AppstoreOutlined, AuditOutlined, CloudServerOutlined, DeploymentUnitOutlined, FileAddOutlined, GlobalOutlined, LogoutOutlined, MenuFoldOutlined, MenuUnfoldOutlined, UserOutlined } from '@ant-design/icons';
import { Avatar, Button, Dropdown, Layout, Menu, Select, Space, Tag, Typography } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { useI18n } from '../i18n';
import { runtimeConfig } from '../runtimeConfig';
import type { Locale, UserRole } from '../types';

const { Header, Sider, Content } = Layout;

export function AppShell() {
  const [collapsed, setCollapsed] = useState(() => window.innerWidth < 1100);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const location = useLocation(); const navigate = useNavigate();
  const { session, logout, setRole } = useAuth(); const { t, locale, setLocale } = useI18n();
  useEffect(() => { const onResize = () => window.innerWidth < 960 && setCollapsed(true); window.addEventListener('resize', onResize); return () => window.removeEventListener('resize', onResize); }, []);
  const selected = location.pathname.startsWith('/applications') ? '/applications' : location.pathname.startsWith('/submit') ? '/submit' : location.pathname.startsWith('/audit') ? '/audit' : '/overview';
  const menuItems = useMemo(() => [
    { key: '/overview', icon: <AppstoreOutlined />, label: t('overview') },
    { key: '/applications', icon: <DeploymentUnitOutlined />, label: t('applications') },
    ...(runtimeConfig.features.submit ? [{ key: '/submit', icon: <FileAddOutlined />, label: t('submitApplication') }] : []),
    ...(runtimeConfig.features.audit && session?.role === 'admin' ? [{ key: '/audit', icon: <AuditOutlined />, label: t('audit') }] : []),
  ], [session?.role, t]);
  return (
    <Layout className="app-layout">
      <Sider width={236} collapsedWidth={72} collapsed={collapsed} trigger={null} className="app-sider">
        <div className="brand" onClick={() => navigate('/overview')}>
          <div className="brand-mark"><span /></div>
          {!collapsed && <div><strong>Spark</strong><small>Control Center</small></div>}
        </div>
        <div className="environment-pill"><span className="live-dot" />{!collapsed && (runtimeConfig.dataMode === 'mock' ? t('mocked') : runtimeConfig.cluster.name)}</div>
        <Menu theme="dark" mode="inline" selectedKeys={[selected]} items={menuItems} onClick={({ key }) => navigate(key)} />
      </Sider>
      <Layout>
        <Header className="topbar">
          <Button type="text" className="collapse-button" icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />} onClick={() => setCollapsed(!collapsed)} />
          <div className="topbar-context">
            <Space size={8}><CloudServerOutlined className="context-icon" /><Typography.Text type="secondary">{t('cluster')}</Typography.Text><strong>{runtimeConfig.cluster.name}</strong></Space>
            <Select className="namespace-select" defaultValue="all" options={[{ value: 'all', label: t('allNamespaces') }, ...runtimeConfig.cluster.namespaces.map((value) => ({ value, label: value }))]} />
          </div>
          <div className="topbar-actions">
            <Select aria-label={t('language')} value={locale} onChange={(value: Locale) => setLocale(value)} suffixIcon={<GlobalOutlined />} options={[{ value: 'zh-CN', label: '中文' }, { value: 'en-US', label: 'EN' }]} />
            <Dropdown
              placement="bottomRight"
              open={userMenuOpen}
              onOpenChange={setUserMenuOpen}
              menu={{ items: [
                { key: 'label', label: <Typography.Text type="secondary">{t('role')}</Typography.Text>, disabled: true },
                ...(runtimeConfig.auth.mode === 'mock' ? (['viewer', 'operator', 'admin'] as UserRole[]).map((role) => ({ key: role, label: role, icon: session?.role === role ? <span className="menu-check">✓</span> : <span /> })) : [{ key: 'current-role', label: session?.role ?? 'viewer', disabled: true }]),
                { type: 'divider' }, { key: 'logout', label: t('signOut'), icon: <LogoutOutlined />, danger: true },
              ], onClick: ({ key }) => { setUserMenuOpen(false); if (key === 'logout') { logout(); navigate('/login'); } else if (key !== 'label') setRole(key as UserRole); } }}
            >
              <Button type="text" className="user-button"><Avatar size={30} icon={<UserOutlined />} /><span className="user-copy"><b>{session?.username}</b><Tag bordered={false}>{session?.role}</Tag></span></Button>
            </Dropdown>
          </div>
        </Header>
        <Content className="page-content"><Outlet /></Content>
      </Layout>
    </Layout>
  );
}
