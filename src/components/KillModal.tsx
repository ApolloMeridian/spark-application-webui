import { App, Input, Modal, Typography } from 'antd';
import { useState } from 'react';
import { useAuth } from '../auth';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import type { SparkApplication } from '../types';

export function ApplicationOperationModal({ application, operation, open, onClose, onCompleted }: { application: SparkApplication; operation: 'kill' | 'delete'; open: boolean; onClose: () => void; onCompleted: () => void }) {
  const [reason, setReason] = useState(''); const [loading, setLoading] = useState(false);
  const { session } = useAuth(); const { t } = useI18n(); const { message } = App.useApp();
  const submit = async () => {
    if (!session) return; setLoading(true);
    try {
      if (operation === 'kill') await sparkService.killApplication(application.namespace, application.name, session.username, reason.trim() || undefined);
      else await sparkService.deleteApplication(application.namespace, application.name, session.username, reason.trim() || undefined);
      message.success(t(operation === 'kill' ? 'killSuccess' : 'deleteSuccess')); setReason(''); onCompleted(); onClose();
    }
    catch (error) { message.error(error instanceof Error ? error.message : 'Operation failed'); }
    finally { setLoading(false); }
  };
  return <Modal title={t(operation === 'kill' ? 'killConfirm' : 'deleteConfirm')} open={open} okText={t(operation === 'kill' ? 'confirmKill' : 'confirmDelete')} cancelText={t('cancel')} okButtonProps={{ danger: true, loading }} onOk={submit} onCancel={onClose} destroyOnHidden>
    <div className="kill-target"><span className="danger-icon">!</span><div><Typography.Text strong>{application.name}</Typography.Text><br /><Typography.Text type="secondary">{application.namespace}</Typography.Text></div></div>
    <Typography.Paragraph type="secondary">{t(operation === 'kill' ? 'killWarning' : 'deleteWarning')}</Typography.Paragraph>
    <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t('reason')} rows={3} maxLength={240} showCount />
  </Modal>;
}
