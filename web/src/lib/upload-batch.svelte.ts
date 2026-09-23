export type Grant = { id: string; expires_at: string; strategy: string };

// Retain shared identity across reactive file rows: plain objects are proxied
// separately at each nested reference, which can split a batch into many grants.
export class UploadBatch {
  name = $state<string | null>(null);
  grant = $state<Grant | null>(null);
  creating: Promise<Grant> | null = null;
}
