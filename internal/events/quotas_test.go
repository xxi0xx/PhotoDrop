package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"photodrop/internal/database"
	"testing"
)

func TestTypedQuotaValidationAndExactPersistence(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db)
	for _, field := range []string{"max_photos", "max_videos", "max_photo_file_bytes", "max_video_file_bytes", "max_photo_storage_bytes", "max_video_storage_bytes"} {
		t.Run(field, func(t *testing.T) {
			bound := int64(1 << 50)
			if field == "max_photos" || field == "max_videos" {
				bound = 1000000
			}
			for _, value := range []int64{-1, 0, bound + 1, 1, bound} {
				var in Input
				if err := json.Unmarshal(fmt.Appendf(nil, `{"name":"Quotas","enabled":true,"%s":%d}`, field, value), &in); err != nil {
					t.Fatal(err)
				}
				event, err := store.Create(t.Context(), in)
				if value < 1 || value > bound {
					var v *ValidationError
					if !errors.As(err, &v) || v.Fields[field] == "" {
						t.Fatal(value, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				saved, err := store.Get(t.Context(), event.ID)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(saved)
				var fields map[string]json.RawMessage
				json.Unmarshal(raw, &fields)
				if string(fields[field]) != fmt.Sprint(value) {
					t.Fatal("rounded bytes", string(raw))
				}
				cleared, err := store.Update(t.Context(), event.ID, input())
				if err != nil {
					t.Fatal(err)
				}
				raw, _ = json.Marshal(cleared)
				json.Unmarshal(raw, &fields)
				if string(fields[field]) != "null" {
					t.Fatal("failed to clear limit")
				}
			}
		})
	}
}
