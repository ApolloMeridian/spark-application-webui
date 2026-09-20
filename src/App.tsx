import { ConfigProvider, App as AntApp, Spin } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import enUS from 'antd/locale/en_US';
import { BrowserRouter, Navigate, Outlet, Route, Routes } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth';
import { I18nProvider, useI18n } from './i18n';
import { AppShell } from './components/AppShell';
import { LoginPage } from './pages/LoginPage';
import { OverviewPage } from './pages/OverviewPage';
import { ApplicationsPage } from './pages/ApplicationsPage';
import { ApplicationDetailPage } from './pages/ApplicationDetailPage';
import { AuditPage } from './pages/AuditPage';

function ProtectedRoute() {
  const { session, loading } = useAuth();
  if (loading) return <div className="auth-loading"><Spin size="large" /></div>;
  return session ? <Outlet /> : <Navigate to="/login" replace />;
}

function ThemedApp() {
  const { locale } = useI18n();
  return (
    <ConfigProvider
      locale={locale === 'zh-CN' ? zhCN : enUS}
      theme={{
        token: { colorPrimary: '#2868f0', colorSuccess: '#12a878', colorWarning: '#e5a11a', colorError: '#e44c55', borderRadius: 8, colorBgLayout: '#f3f6fa', fontFamily: 'Inter, "Segoe UI", "PingFang SC", sans-serif' },
        components: { Layout: { siderBg: '#10213d', headerBg: '#ffffff' }, Table: { headerBg: '#f6f8fb', headerColor: '#526079' } },
      }}
    >
      <AntApp>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route element={<ProtectedRoute />}>
              <Route element={<AppShell />}>
                <Route path="/overview" element={<OverviewPage />} />
                <Route path="/applications" element={<ApplicationsPage />} />
                <Route path="/applications/:namespace/:name" element={<ApplicationDetailPage />} />
                <Route path="/audit" element={<AuditPage />} />
              </Route>
            </Route>
            <Route path="*" element={<Navigate to="/overview" replace />} />
          </Routes>
        </BrowserRouter>
      </AntApp>
    </ConfigProvider>
  );
}

export function App() {
  return <I18nProvider><AuthProvider><ThemedApp /></AuthProvider></I18nProvider>;
}
