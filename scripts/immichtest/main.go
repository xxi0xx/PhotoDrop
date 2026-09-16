// Test-only object providers and a fault-injecting proxy to a REAL Immich API.
// This executable is never included in the PhotoDrop production image.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"photodrop/internal/testutil/s3test"
)

type faultState struct {
	FailNext   bool `json:"fail_next"`
	LoseNext   bool `json:"lose_next"`
	DelayMS    int  `json:"delay_ms"`
	Uploads    int  `json:"uploads"`
	Created    int  `json:"created"`
	Duplicates int  `json:"duplicates"`
}

func main() {
	for _, address := range []string{":18092", ":18093"} {
		go func() { log.Fatal(http.ListenAndServe(address, s3test.New("http://localhost:8083"))) }()
	}
	upstream, _ := url.Parse("http://immich-server:2283")
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var mu sync.Mutex
	var state faultState
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.Request.Method != "POST" || r.Request.URL.Path != "/api/assets" || r.StatusCode >= 300 {
			return nil
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		r.Body.Close()
		if err != nil {
			return err
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		var outcome struct {
			Status string `json:"status"`
		}
		json.Unmarshal(b, &outcome)
		mu.Lock()
		defer mu.Unlock()
		if outcome.Status == "created" {
			state.Created++
		}
		if outcome.Status == "duplicate" {
			state.Duplicates++
		}
		if state.LoseNext {
			state.LoseNext = false
			return errors.New("test-only response loss after real Immich acceptance")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "test proxy unavailable", 503) }
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__test/control" {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == "POST" {
				var input faultState
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
					http.Error(w, "invalid control", 400)
					return
				}
				state.FailNext = input.FailNext
				state.LoseNext = input.LoseNext
				state.DelayMS = input.DelayMS
			}
			json.NewEncoder(w).Encode(state)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			mu.Lock()
			state.Uploads++
			fail, delay := state.FailNext, state.DelayMS
			state.FailNext = false
			mu.Unlock()
			if fail {
				http.Error(w, "test-only forced upload failure", 503)
				return
			}
			if delay > 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Duration(delay) * time.Millisecond):
				}
			}
		}
		proxy.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServe(":2284", handler))
}
