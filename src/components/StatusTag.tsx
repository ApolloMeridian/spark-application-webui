import { Tag } from 'antd';
import type { ApplicationState } from '../types';
import { stateColor } from '../utils';

export function StatusTag({ state }: { state: ApplicationState }) {
  return <Tag className="status-tag" color={stateColor[state]}><span className="status-dot" />{state.replace('_', ' ')}</Tag>;
}
