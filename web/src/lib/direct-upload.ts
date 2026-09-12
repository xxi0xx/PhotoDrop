export type UploadPlan = { strategy: 'direct'; method: 'PUT'; url: string; headers: Record<string,string>; expires_at: string };
export type Prepared = { asset: { id: string; status: string }; upload: UploadPlan };
export type DirectAttempt = { requestID: string; assetID?: string };
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
  const canceled = () => { if (ops.canceled()) throw new Error('Upload canceled. You can retry this photo.'); };
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
      if (putAttempt === 1) throw new Error('Upload failed. Check your connection and retry this photo.');
      ops.stage('preparing'); plan = (await ops.authorize(attempt.assetID!)).upload;
      continue;
    }
    await complete(); return;
  }
}
