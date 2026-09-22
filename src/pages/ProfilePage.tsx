import { LockOutlined, SaveOutlined, UserOutlined } from '@ant-design/icons';
import { App, Button, Card, Col, Form, Input, Row } from 'antd';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { PageHeader } from '../components/PageHeader';
import { useI18n } from '../i18n';
import { userService } from '../userService';

export function ProfilePage() {
  const { session, updateSession, logout } = useAuth(); const { t } = useI18n(); const { message } = App.useApp(); const navigate = useNavigate();
  const saveProfile = async (values: { displayName: string; email: string }) => {
    try { const result = await userService.updateProfile(values); updateSession(result.user); message.success(t('profileUpdated')); } catch (error) { message.error(error instanceof Error ? error.message : String(error)); }
  };
  const changePassword = async (values: { currentPassword: string; newPassword: string; confirmPassword: string }) => {
    try {
      await userService.updateProfile({ currentPassword: values.currentPassword, newPassword: values.newPassword });
      message.success(t('passwordChangedLoginAgain')); await logout(); navigate('/login');
    } catch (error) { message.error(error instanceof Error ? error.message : String(error)); }
  };
  return <>
    <PageHeader title={t('myProfile')} subtitle={t('profileHint')} />
    <Row gutter={[20, 20]}>
      <Col xs={24} xl={12}><Card className="panel-card" title={<><UserOutlined /> {t('accountInformation')}</>}>
        <Form layout="vertical" initialValues={{ username: session?.username, displayName: session?.displayName, email: session?.email }} onFinish={saveProfile}>
          <Form.Item name="username" label={t('username')}><Input disabled /></Form.Item>
          <Form.Item name="displayName" label={t('displayName')}><Input maxLength={100} /></Form.Item>
          <Form.Item name="email" label={t('email')} rules={[{ type: 'email' }]}><Input /></Form.Item>
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />}>{t('save')}</Button>
        </Form>
      </Card></Col>
      {session?.authSource !== 'oidc' && <Col xs={24} xl={12}><Card className="panel-card" title={<><LockOutlined /> {t('changePassword')}</>}>
        <Form layout="vertical" onFinish={changePassword}>
          <Form.Item name="currentPassword" label={t('currentPassword')} rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item>
          <Form.Item name="newPassword" label={t('newPassword')} rules={[{ required: true }, { min: 8 }, { max: 128 }]}><Input.Password autoComplete="new-password" /></Form.Item>
          <Form.Item name="confirmPassword" label={t('confirmPassword')} dependencies={['newPassword']} rules={[{ required: true }, ({ getFieldValue }) => ({ validator: (_, value) => !value || getFieldValue('newPassword') === value ? Promise.resolve() : Promise.reject(new Error(t('passwordMismatch'))) })]}><Input.Password autoComplete="new-password" /></Form.Item>
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />}>{t('changePassword')}</Button>
        </Form>
      </Card></Col>}
    </Row>
  </>;
}
