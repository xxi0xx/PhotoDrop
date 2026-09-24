// Test-only inspection/seeding tool for disposable release validation databases.
// Never copied into the production image or exposed as an HTTP endpoint.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"photodrop/internal/database"
)

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "seed" && os.Args[1] != "snapshot") {
		panic("usage: releasestate seed|snapshot DISPOSABLE_DATA_DIR")
	}
	db, err := database.Open(context.Background(), os.Args[2])
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if os.Args[1] == "seed" {
		_, err = db.Exec(`
INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'release-test','http://example.test','2026-01-01T00:00:00Z');
INSERT INTO immich_event_imports(id,event_id,target_id,album_name,album_marker,album_state,immich_album_id,created_at,updated_at)
VALUES(1,1,1,'Restore fixture','release-fixture-marker','ready','test-album','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
INSERT INTO immich_asset_imports(event_import_id,asset_id,immich_asset_id,status,attempt_count,created_at,updated_at,imported_at)
SELECT 1,id,'test-remote-asset','imported',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'
FROM assets WHERE storage_provider='local' AND status='ready' LIMIT 1;
INSERT INTO integration_jobs(event_import_id,status,mode,created_at,finished_at)
VALUES(1,'completed','new','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`)
		if err != nil {
			panic(err)
		}
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		panic("SQLite integrity failure")
	}
	result := map[string][]map[string]any{}
	// Excludes credentials/admin session identifiers from output. HTTP checks test
	// the preserved admin session separately without logging it.
	for _, table := range []string{"events", "assets", "storage_backends", "schema_migrations", "immich_targets", "immich_event_imports", "immich_asset_imports", "integration_jobs"} {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			panic(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			panic(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				panic(err)
			}
			row := map[string]any{}
			for i, name := range columns {
				if name != "upload_session_id" {
					row[name] = values[i]
				}
			}
			result[table] = append(result[table], row)
		}
		if err := rows.Err(); err != nil {
			panic(err)
		}
		rows.Close()
	}
	b, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
