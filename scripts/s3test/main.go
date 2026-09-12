// Test-only S3 endpoint for browser validation. Never used by production Compose.
package main

import (
	"flag"
	"log"
	"net/http"
	"photodrop/internal/testutil/s3test"
	"time"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:9090", "test endpoint")
	origin := flag.String("origin", "http://localhost:8081", "allowed browser origin")
	delay := flag.Duration("delay", 2*time.Second, "delay PUT bodies to observe progress")
	drop := flag.Int("drop-put-responses", 0, "simulate lost successful PUT responses")
	flag.Parse()
	s := s3test.New(*origin)
	s.Delay(*delay)
	s.DropPUTResponses(*drop)
	log.Printf("Test-only S3 endpoint listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, s))
}
