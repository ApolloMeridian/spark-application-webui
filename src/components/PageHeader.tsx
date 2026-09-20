import { Breadcrumb, Typography } from 'antd';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

export function PageHeader({ title, subtitle, extra, back }: { title: ReactNode; subtitle?: ReactNode; extra?: ReactNode; back?: { label: string; to: string } }) {
  return (
    <div className="page-header">
      <div>
        {back && <Breadcrumb className="page-breadcrumb" items={[{ title: <Link to={back.to}>{back.label}</Link> }, { title }]} />}
        <Typography.Title level={2}>{title}</Typography.Title>
        {subtitle && <Typography.Text type="secondary">{subtitle}</Typography.Text>}
      </div>
      {extra && <div className="page-header-extra">{extra}</div>}
    </div>
  );
}
