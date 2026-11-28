export type DestinationAccount = { id: number; provider: string; source: 'config' | 'linked'; remote_email?: string; status: 'active' | 'needs_relink' | 'revoked'; status_reason?: string; last_validated_at?: string; target: string };
export type DestinationTarget = { key: string; linkable: boolean; reason?: string };
export type Destinations = { accounts: DestinationAccount[]; targets: DestinationTarget[] };

// Each result code the link callback can return maps to one fixed message.
const results: Record<string, { ok: boolean; text: string }> = {
  linked: { ok: true, text: 'Your Immich account is linked. Your events can now send photos to it.' },
  unlinked: { ok: true, text: 'Your Immich account is unlinked, and PhotoDrop deleted its API key in Immich.' },
  link_failed: { ok: false, text: "Linking didn't finish. If you have no Immich account yet, sign in to Immich with your identity provider once first, or ask the Immich admin to create your account. Then try again." },
  link_wrong_user: { ok: false, text: 'Immich signed in a different person than you. Nothing was linked. Sign out of Immich and your identity provider, then try again as yourself.' },
  link_subject_mismatch: { ok: false, text: 'Your Immich user has your email but a different identity. Nothing was linked. Ask the administrator to set this server to match by email (PHOTODROP_IMMICH_<KEY>_LINK_MATCH=email).' },
  link_in_use: { ok: false, text: 'This Immich user is already linked to another PhotoDrop host. Nothing was linked.' },
  link_version: { ok: false, text: 'This Immich server must be upgraded to 3.4.2 or later before accounts can be linked.' },
};

export function linkResultMessage(code: string | null): { ok: boolean; text: string } | null {
  if (!code) return null;
  return results[code] ?? results.link_failed;
}

const reasons: Record<string, string> = {
  secrets_key: 'Linking is unavailable until the administrator configures a secrets key.',
  base_url: 'Linking is unavailable until the administrator sets PHOTODROP_BASE_URL.',
  no_url: 'This server has no API address configured.',
  config_account: 'This server uses an API key from the deployment configuration.',
  config_key_ignored: 'This server has a configured key that is being ignored.',
  immich_version: 'This Immich server must be upgraded to 3.4.2 or later before accounts can be linked.',
  immich_unavailable: "Immich can't be reached right now. Try again later.",
};

export function targetReasonMessage(reason?: string): string {
  return (reason && reasons[reason]) || 'Linking is not available for this server.';
}
