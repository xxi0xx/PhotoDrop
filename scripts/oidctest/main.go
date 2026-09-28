// Disposable OIDC fixture. This binary is never included in the runtime image.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"photodrop/internal/testutil/oidctest"
	"time"
)

func main() {
	listen := flag.String("listen", ":18995", "test listener")
	issuer := flag.String("issuer", "http://localhost:18995/application/o/drop/", "test issuer")
	flag.Parse()
	p, err := oidctest.New(*issuer)
	if err != nil {
		log.Fatal("fixture key initialization failed")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/__test/faults", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var f oidctest.Faults
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&f) != nil {
				http.Error(w, "invalid test fault", 400)
				return
			}
			p.SetFaults(f)
		}
		n, exchanges, jwks := p.Counts()
		json.NewEncoder(w).Encode(map[string]int{"requests": n, "exchanges": exchanges, "jwks": jwks})
	})
	mux.Handle("/", p)
	srv := http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
