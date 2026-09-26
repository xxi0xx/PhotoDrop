export type UploadPlan = { strategy: 'direct'; method: 'PUT'; url: string; headers: Record<string,string>; expires_at: string };
export type Prepared = { asset: { id: string; status: string }; upload: UploadPlan };
export type DirectAttempt = { requestID: string; assetID?: string };
type GrantedAttempt = { attempt: DirectAttempt; grant?: { id: string } };

// Only an authoritative missing grant/attempt or denied new authorization lets
// the UI forget an ID. A failed verification may still have committed remotely.
export function recoverUploadGrant(item: GrantedAttempt, error: unknown, stage: string): boolean {
  if (typeof error !== 'object' || error === null || !('code' in error) || !('status' in error)) return false;
  const removed = error.status === 404 && ['upload_session_not_found', 'asset_not_found'].includes(String(error.code));
  const exhausted = stage !== 'verifying' && error.status === 409 && ['session_expired', 'session_quota'].includes(String(error.code));
  if (!removed && !exhausted) return false;
  item.grant = undefined;
  item.attempt = { requestID: item.attempt.requestID };
  return true;
}
export type DirectOps = {
  prepare(): Promise<Prepared>;
  authorize(id: string): Promise<Prepared>;
  complete(id: string): Promise<unknown>;
  put(plan: UploadPlan): Promise<void>;
  stage(value: 'preparing' | 'uploading' | 'verifying'): void;
  canceled(): boolean;
};
function canUpload(error: unknown): boolean {
  return typeof error === 'object' && error !== null && 'code' in error &&
    ['object_missing','size_mismatch','unsupported_image','empty_file','object_changed'].includes(String(error.code));
}

// One page-session attempt keeps one asset and key. An uncertain PUT or lost
// completion response always tries verification first; each run permits at most
// two PUTs. Transient server/storage errors do not trigger another media upload.
export async function runDirect(attempt: DirectAttempt, ops: DirectOps): Promise<void> {
  const canceled = () => { if (ops.canceled()) throw new Error('Upload canceled. You can retry this file.'); };
  const complete = async () => { canceled(); ops.stage('verifying'); await ops.complete(attempt.assetID!); };
  let plan: UploadPlan;
  if (attempt.assetID) {
    try { await complete(); return; } catch (error) { if (!canUpload(error)) throw error; }
    canceled(); ops.stage('preparing'); plan = (await ops.authorize(attempt.assetID)).upload;
  } else {
    canceled(); ops.stage('preparing');
    const prepared = await ops.prepare(); attempt.assetID = prepared.asset.id;
    if (prepared.asset.status === 'ready') return;
    plan = prepared.upload;
  }
  for (let putAttempt = 0; putAttempt < 2; putAttempt++) {
    canceled(); ops.stage('uploading');
    try { await ops.put(plan); }
    catch (error) {
      canceled();
      try { await complete(); return; }
      catch (verification) { if (!canUpload(verification)) throw verification; }
      if (putAttempt === 1) throw new Error('Upload failed. Check your connection and retry this file.');
      ops.stage('preparing'); plan = (await ops.authorize(attempt.assetID!)).upload;
      continue;
    }
    await complete(); return;
  }
}
