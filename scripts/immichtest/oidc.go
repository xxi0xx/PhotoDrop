package main

import (
	"encoding/json"
	"log"
	"net/http"

	"photodrop/internal/testutil/oidctest"
)

// The test identity provider shares one network namespace with PhotoDrop and
// Immich, so every party (and the test runner, through the published port)
// reaches it at the same loopback issuer. PhotoDrop and Immich are separate
// clients with exact redirect allowlists, as in a real authentik deployment.
const (
	oidcIssuer         = "http://localhost:18997/application/o/drop/"
	immichClientID     = "immich-test"
	immichClientSecret = "disposable-immich-client-secret"
)

func serveOIDC() {
	p, err := oidctest.New(oidcIssuer)
	if err != nil {
		log.Fatal("test identity provider key initialization failed")
	}
	p.SetClient(oidctest.ClientID, oidctest.Client{Secret: oidctest.Secret, RequireNonce: true, Redirects: []string{"http://localhost:8083/api/admin/oidc/callback"}})
	p.SetClient(immichClientID, oidctest.Client{Secret: immichClientSecret, Redirects: []string{
		"http://localhost:8083/api/admin/destinations/immich/callback",
		"http://localhost:22830/auth/login",
	}})
	mux := http.NewServeMux()
	// POST sets the identity issued next (oidctest.Faults); GET reports counts
	// and every token exchange, including the redirect_uri each client sent.
	mux.HandleFunc("/__test/oidc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var f oidctest.Faults
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&f) != nil {
				http.Error(w, "invalid test identity", 400)
				return
			}
			p.SetFaults(f)
		}
		requests, exchanges, jwks := p.Counts()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"requests": requests, "exchanges": exchanges, "jwks": jwks, "history": p.Exchanges()})
	})
	mux.Handle("/", p)
	log.Fatal(http.ListenAndServe(":18997", mux))
}
