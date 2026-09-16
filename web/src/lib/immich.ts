export type ImmichTarget = { id: number; key: string; available: boolean };
export type ImmichStatus = {
  active_target: string; targets: ImmichTarget[]; target?: ImmichTarget;
  album_name: string; album_id?: string;
  total: number; imported: number; duplicate: number; failed: number; pending: number; new: number;
  job?: { id: number; status: 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'; error: string; cancel_requested: boolean };
};
export function importView(status: ImmichStatus, busy: boolean, deleting = false) {
  const active = status.job?.status === 'queued' || status.job?.status === 'running';
  const disabled = busy || deleting || active || !status.target?.available;
  return {
    active, canSend: !disabled && status.new + status.pending > 0,
    canRetry: !disabled && status.failed > 0,
    accounted: status.imported + status.duplicate,
    sendCount: status.new + status.pending,
    label: active ? (status.job?.cancel_requested ? 'Cancelling import…' : 'Import in progress…') : status.job?.status === 'completed' ? 'Import completed' : status.job?.status === 'cancelled' ? 'Import cancelled' : status.job?.status === 'failed' ? 'Import needs attention' : 'Ready to import',
  };
}
