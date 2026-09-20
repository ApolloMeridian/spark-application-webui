import { CloudServerOutlined, DeploymentUnitOutlined, LockOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, Select, Space, Tag, Typography } from 'antd';
import { Navigate, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { useI18n } from '../i18n';
import { runtimeConfig } from '../runtimeConfig';
import type { UserRole } from '../types';

export function LoginPage() {
  const { session, login, startOidcLogin } = useAuth(); const { t } = useI18n(); const navigate = useNavigate();
  if (session) return <Navigate to="/overview" replace />;
  const submit = ({ username, role }: { username: string; role: UserRole }) => { login({ username, role }); navigate('/overview'); };
  return <div className="login-page">
    <div className="login-visual">
      <div className="login-orbit orbit-one" /><div className="login-orbit orbit-two" />
      <div className="login-visual-content">
        <div className="login-logo"><div className="brand-mark large"><span /></div><span>{runtimeConfig.appName}</span></div>
        <Typography.Title>Operate Spark.<br />Understand Kubernetes.</Typography.Title>
        <Typography.Paragraph>One focused control plane for applications, resources, scheduling, events and operator actions.</Typography.Paragraph>
        <Space size={24} wrap>
          <div className="login-feature"><DeploymentUnitOutlined /><span>Application lifecycle</span></div>
          <div className="login-feature"><CloudServerOutlined /><span>GKE scheduling</span></div>
          <div className="login-feature"><SafetyCertificateOutlined /><span>Role-based access</span></div>
        </Space>
      </div>
    </div>
    <div className="login-panel">
      <Card className="login-card" variant="borderless">
        <Tag color="blue" bordered={false}><LockOutlined /> {runtimeConfig.auth.mode === 'oidc' ? 'Keycloak OIDC' : t('mocked')}</Tag>
        <Typography.Title level={2}>{t('loginTitle')}</Typography.Title>
        <Typography.Paragraph type="secondary">{t('loginHint')}</Typography.Paragraph>
        {runtimeConfig.auth.mode === 'oidc' ? <div className="oidc-login">
          <Typography.Paragraph type="secondary">{runtimeConfig.auth.oidc.issuerUrl}</Typography.Paragraph>
          <Button type="primary" size="large" block onClick={startOidcLogin}>{t('login')}</Button>
          <div className="login-footer">Authorization Code Flow · PKCE · HttpOnly session</div>
        </div> : <Form layout="vertical" size="large" initialValues={{ username: 'jeremy', role: 'operator' }} onFinish={submit}>
          <Form.Item name="username" label={t('username')} rules={[{ required: true }]}><Input prefix={<UserOutlined />} autoFocus /></Form.Item>
          <Form.Item name="role" label={t('role')}><Select options={[
            { value: 'viewer', label: 'viewer — Read only' }, { value: 'operator', label: 'operator — View & kill' }, { value: 'admin', label: 'admin — Full access' },
          ]} /></Form.Item>
          <Button type="primary" htmlType="submit" block>{t('login')}</Button>
        </Form>}
        {runtimeConfig.auth.mode === 'mock' && <div className="login-footer">OIDC simulation · No credentials are sent</div>}
      </Card>
    </div>
  </div>;
}
