import { CloudServerOutlined, DeploymentUnitOutlined, GlobalOutlined, LockOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Divider, Form, Input, Select, Space, Tag, Typography } from 'antd';
import { useState } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../auth';
import { useI18n } from '../i18n';
import { runtimeConfig } from '../runtimeConfig';
import type { Locale, UserRole } from '../types';

export function LoginPage() {
  const { session, login, loginMock, startOidcLogin } = useAuth(); const { t, locale, setLocale } = useI18n(); const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [submitting, setSubmitting] = useState(false); const [error, setError] = useState('');
  if (session) return <Navigate to="/overview" replace />;
  const submitLocal = async ({ username, password }: { username: string; password: string }) => {
    setSubmitting(true); setError('');
    try { await login(username, password); navigate('/overview'); } catch (reason) { setError(reason instanceof Error ? reason.message : t('loginFailed')); } finally { setSubmitting(false); }
  };
  const submitMock = ({ username, role }: { username: string; role: UserRole }) => { loginMock(username, role); navigate('/overview'); };
  return <div className="login-page">
    <div className="login-visual">
      <div className="login-orbit orbit-one" /><div className="login-orbit orbit-two" />
      <div className="login-visual-content">
        <div className="login-logo"><div className="brand-mark large"><span /></div><span>{runtimeConfig.appName}</span></div>
        <Typography.Title>Operate Spark.<br />Understand Kubernetes.</Typography.Title>
        <Typography.Paragraph>One focused control plane for applications, resources, scheduling, events and operator actions.</Typography.Paragraph>
        <Space size={24} wrap>
          <div className="login-feature"><DeploymentUnitOutlined /><span>Application lifecycle</span></div>
          <div className="login-feature"><CloudServerOutlined /><span>Kubernetes scheduling</span></div>
          <div className="login-feature"><SafetyCertificateOutlined /><span>Role-based access</span></div>
        </Space>
      </div>
    </div>
    <div className="login-panel">
      <Card className="login-card" variant="borderless">
        <div className="login-language"><Select aria-label={t('language')} prefix={<GlobalOutlined />} value={locale} onChange={(value) => setLocale(value as Locale)} options={[{ value: 'zh-CN', label: '简体中文' }, { value: 'en-US', label: 'English' }]} /></div>
        <Tag color="blue" bordered={false}><LockOutlined /> {runtimeConfig.auth.mode === 'local' ? t('localAccount') : runtimeConfig.auth.mode === 'oidc' ? 'Keycloak OIDC' : t('mocked')}</Tag>
        <Typography.Title level={2}>{t('loginTitle')}</Typography.Title>
        <Typography.Paragraph type="secondary">{t(runtimeConfig.auth.mode === 'mock' ? 'mockLoginHint' : runtimeConfig.auth.mode === 'oidc' ? 'oidcLoginHint' : 'loginHint')}</Typography.Paragraph>
        {(error || searchParams.get('oidcError')) && <Alert type="error" showIcon message={error || t('oidcLoginFailed')} className="login-error" />}
        {runtimeConfig.auth.mode === 'oidc' ? <div className="oidc-login">
          <Typography.Paragraph type="secondary">{runtimeConfig.auth.oidc.issuerUrl}</Typography.Paragraph>
          <Button type="primary" size="large" block onClick={startOidcLogin}>{t('loginWithProvider').replace('{provider}', runtimeConfig.auth.oidc.providerName)}</Button>
          <div className="login-footer">Authorization Code Flow · PKCE · HttpOnly session</div>
        </div> : runtimeConfig.auth.mode === 'local' ? <Form layout="vertical" size="large" initialValues={{ username: 'admin' }} onFinish={submitLocal}>
          <Form.Item name="username" label={t('username')} rules={[{ required: true }]}><Input prefix={<UserOutlined />} autoComplete="username" autoFocus /></Form.Item>
          <Form.Item name="password" label={t('password')} rules={[{ required: true }]}><Input.Password prefix={<LockOutlined />} autoComplete="current-password" /></Form.Item>
          <Button type="primary" htmlType="submit" block loading={submitting}>{t('login')}</Button>
        </Form> : <Form layout="vertical" size="large" initialValues={{ username: 'demo-user', role: 'viewer' }} onFinish={submitMock}>
          <Form.Item name="username" label={t('username')} rules={[{ required: true }]}><Input prefix={<UserOutlined />} autoFocus /></Form.Item>
          <Form.Item name="role" label={t('role')}><Select options={[{ value: 'viewer', label: 'viewer — Read only' }, { value: 'admin', label: 'admin — Full access' }]} /></Form.Item>
          <Button type="primary" htmlType="submit" block>{t('login')}</Button>
        </Form>}
        {runtimeConfig.auth.mode === 'local' && runtimeConfig.auth.oidc.enabled && <>
          <Divider plain>{t('or')}</Divider>
          <Button size="large" block icon={<SafetyCertificateOutlined />} onClick={startOidcLogin}>{t('loginWithProvider').replace('{provider}', runtimeConfig.auth.oidc.providerName)}</Button>
        </>}
        <div className="login-footer">{runtimeConfig.auth.mode === 'local' ? t('localLoginFooter') : runtimeConfig.auth.mode === 'mock' ? 'Local simulation · No credentials are sent' : ''}</div>
      </Card>
    </div>
  </div>;
}
