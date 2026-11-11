import { App, Input, Modal, Typography } from 'antd';
import { useState } from 'react';
import { useAuth } from '../auth';
import { useI18n } from '../i18n';
import { sparkService } from '../service';
import type { SparkApplication } from '../types';

export function ApplicationOperationModal({ application, operation, open, onClose, onCompleted }: { application: SparkApplication; operation: 'kill' | 'delete'; open: boolean; onClose: () => void; onCompleted: () => void }) {
  const [reason, setReason] = useState(''); const [loading, setLoading] = useState(false);
  const { session } = useAuth(); const { t } = useI18n(); const { message } = App.useApp();
  const purgeHistory = operation === 'delete' && application.historical;
  const submit = async () => {
    if (!session || (purgeHistory && session.role !== 'admin')) return;
    setLoading(true);
    try {
      if (operation === 'kill') await sparkService.killApplication(application.namespace, application.id, session.username, reason.trim() || undefined);
      else if (purgeHistory) await sparkService.purgeApplicationHistory(application.namespace, application.id, session.username, reason.trim() || undefined);
      else await sparkService.deleteApplication(application.namespace, application.id, session.username, reason.trim() || undefined);
      message.success(t(operation === 'kill' ? 'killSuccess' : purgeHistory ? 'purgeHistorySuccess' : 'deleteSuccess')); setReason(''); onCompleted(); onClose();
    }
    catch (error) { message.error(error instanceof Error ? error.message : 'Operation failed'); }
    finally { setLoading(false); }
  };
  return <Modal title={t(operation === 'kill' ? 'killConfirm' : purgeHistory ? 'purgeHistoryConfirm' : 'deleteConfirm')} open={open} okText={t(operation === 'kill' ? 'confirmKill' : purgeHistory ? 'purgeHistory' : 'confirmDelete')} cancelText={t('cancel')} okButtonProps={{ danger: true, loading, disabled: purgeHistory && session?.role !== 'admin' }} onOk={submit} onCancel={onClose} destroyOnHidden>
    <div className="kill-target"><span className="danger-icon">!</span><div><Typography.Text strong>{application.name}</Typography.Text><br /><Typography.Text type="secondary">{application.namespace}</Typography.Text></div></div>
    <Typography.Paragraph type="secondary">{t(operation === 'kill' ? 'killWarning' : purgeHistory ? 'purgeHistoryWarning' : 'deleteWarning')}</Typography.Paragraph>
    <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t('reason')} rows={3} maxLength={240} showCount />
  </Modal>;
}
