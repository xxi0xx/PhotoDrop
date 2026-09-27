export type ImmichTarget = { id: number; key: string; available: boolean; public_url?: string };
export type ImmichTargets = { active_target: string; targets: ImmichTarget[] };
export function initialTarget(catalog: ImmichTargets): string {
  return catalog.targets.find(one => one.key === catalog.active_target)?.key ?? catalog.targets.find(one => one.available)?.key ?? catalog.targets[0]?.key ?? '';
}
// null means the administrator has never edited the suggested album name.
export function suggestedAlbum(eventName: string, edited: string | null): string { return edited ?? eventName; }
export type ImmichStatus = {
  active_target: string; targets: ImmichTarget[]; target?: ImmichTarget;
  album_name: string; album_id?: string; album_state?: string; album_url?: string;
  total: number; imported: number; duplicate: number; failed: number; pending: number; new: number;
  job?: { id: number; status: 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'; error: string; cancel_requested: boolean };
};
export function importView(status: ImmichStatus, busy: boolean, deleting = false) {
  const active = status.job?.status === 'queued' || status.job?.status === 'running';
  const disabled = busy || deleting || active || !status.target?.available;
  return {
    active, canSend: !disabled && status.new + status.pending > 0,
    canRetry: !disabled && (status.failed > 0 || (status.job?.status === 'failed' && status.pending > 0)),
    canProvision: !disabled && !status.album_id && status.imported + status.duplicate + status.failed + status.pending === 0,
    albumLabel: status.album_id ? 'Album ready' : active ? 'Preparing Immich album…' : status.job?.status === 'failed' ? 'Album setup needs attention' : status.job?.status === 'cancelled' ? 'Album setup cancelled' : 'No Immich album yet',
    accounted: status.imported + status.duplicate,
    sendCount: status.new + status.pending,
    label: active ? (status.job?.cancel_requested ? 'Cancelling import…' : 'Import in progress…') : status.job?.status === 'completed' ? 'Import completed' : status.job?.status === 'cancelled' ? 'Import cancelled' : status.job?.status === 'failed' ? 'Import needs attention' : 'Ready to import',
  };
}
