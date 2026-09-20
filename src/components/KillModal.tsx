import { App, Input, Modal, Typography } from 'antd';
import { useState } from 'react';
import { useAuth } from '../auth';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import type { SparkApplication } from '../types';

export function KillModal({ application, open, onClose, onKilled }: { application: SparkApplication; open: boolean; onClose: () => void; onKilled: () => void }) {
  const [reason, setReason] = useState(''); const [loading, setLoading] = useState(false);
  const { session } = useAuth(); const { t } = useI18n(); const { message } = App.useApp();
  const submit = async () => {
    if (!session) return; setLoading(true);
    try { await sparkService.killApplication(application.namespace, application.name, session.username, reason.trim() || undefined); message.success(t('killSuccess')); setReason(''); onKilled(); onClose(); }
    catch (error) { message.error(error instanceof Error ? error.message : 'Operation failed'); }
    finally { setLoading(false); }
  };
  return <Modal title={t('killConfirm')} open={open} okText={t('confirmKill')} cancelText={t('cancel')} okButtonProps={{ danger: true, loading }} onOk={submit} onCancel={onClose} destroyOnHidden>
    <div className="kill-target"><span className="danger-icon">!</span><div><Typography.Text strong>{application.name}</Typography.Text><br /><Typography.Text type="secondary">{application.namespace}</Typography.Text></div></div>
    <Typography.Paragraph type="secondary">{t('killWarning')}</Typography.Paragraph>
    <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t('reason')} rows={3} maxLength={240} showCount />
  </Modal>;
}
