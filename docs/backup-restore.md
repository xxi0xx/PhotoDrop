# Backup and restore

Back up the complete persistent state, not just exported photos:

```text
/data/photodrop.db       events, auth/sessions, assets, quotas, backend identities,
                       contributors, migrations, Immich jobs/import mappings
/data/uploads/          local originals, including historical local uploads
/data/photodrop.db-*     SQLite sidecars, when present
```

Preserve the whole directory including journals/WAL/SHM files when present. Save
Compose files, image digest/version and private configuration/credentials separately
in encrypted/access-controlled storage. Backups include personal data and session state.

## Consistent stopped backup

No supported online backup command exists. Stop the server and export CLI processes;
never copy a live SQLite file. Linux default bind-mount procedure:

```sh
docker compose stop
mkdir -p backups
chmod 700 backups
# Choose a new filename. Never overwrite the only known-good backup.
sudo tar --numeric-owner -cpf backups/photodrop-pre-upgrade.tar data
sudo chmod 600 backups/photodrop-pre-upgrade.tar
docker compose up --no-build --wait -d
```

Store a verified off-host copy. Inspect the archive privately and periodically
restore into an isolated deployment. On copy failure, retain original state and
resolve before upgrading. Equivalent stopped snapshots are fine; preserve modes
and ownership. Windows operators need a backup mechanism preserving container
ownership or an isolated Linux backup environment.

## Restore

Stop the app; keep current data as a separate recovery copy. Extract a trusted
backup into an empty, isolated directory, never over a live/existing database:

```sh
mkdir restore-check
sudo tar --numeric-owner -xpf backups/photodrop-pre-upgrade.tar -C restore-check
# Configure a separate Compose project/port with ./restore-check/data:/data.
# Start only that isolated project with matching image and saved configuration.
```

Check health/login, events, public IDs, contributor labels, quotas, backend records,
migration validity and exported media hashes. A test origin changes the URL origin
intentionally; restore the original BASE_URL in production to preserve printed QR
URLs. Run only one server per directory. Restored sessions may still be valid;
rotate the administrator password if backup custody was compromised.

## External storage and Immich

A `/data` backup contains authoritative metadata, **not S3/R2 object bytes**.
Arrange independent bucket backups/versioning. Objects deleted since the backup
may require provider recovery. Stopping PhotoDrop does not revoke outstanding
presigned PUTs: coordinate remote snapshots and retain object history. Never use
broad bucket deletion for restore tests. Preserve historical credentials/addressing
for reads, deletes, export and pending finalization.

Immich needs independent backups. Restored import mappings are bookkeeping, not
proof that remote copies exist. Inspect queued jobs before exposing restored state:
workers resume on startup. Isolate the network or withhold Immich keys during
verification, then restore matching credentials. See [upgrading](upgrading.md)
and [tested scenarios](gate-8-validation.md).
