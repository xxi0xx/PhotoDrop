package abuse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ChallengeAction = "photodrop_upload"
const siteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

var ErrChallenge = errors.New("Please complete the verification and try again.")
var ErrChallengeExpired = errors.New("Verification expired or was already used. Please try again.")
var ErrChallengeUnavailable = errors.New("Verification is temporarily unavailable. Please try again later.")

type Verification struct {
	Success  bool     `json:"success"`
	Hostname string   `json:"hostname"`
	Action   string   `json:"action"`
	Codes    []string `json:"error-codes"`
}
type Verifier interface {
	Verify(context.Context, string) (Verification, error)
}
type Turnstile struct {
	secret string
	client *http.Client
}

func NewTurnstile(secret string) *Turnstile {
	return &Turnstile{secret: secret, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (*Turnstile) String() string     { return "Turnstile verifier (secret redacted)" }
func (t *Turnstile) GoString() string { return t.String() }
func (t *Turnstile) Verify(ctx context.Context, token string) (Verification, error) {
	var result Verification
	if token == "" || len(token) > 2048 {
		return result, ErrChallenge
	}
	// One redemption only. Ambiguous results require a fresh widget token; there
	// is no retry that could turn idempotent verification into two session grants.
	body := url.Values{"secret": {t.secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteverifyURL, strings.NewReader(body.Encode()))
	if err != nil {
		return result, ErrChallengeUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := t.client.Do(req)
	if err != nil {
		return result, ErrChallengeUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, ErrChallengeUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &result) != nil {
		return Verification{}, ErrChallengeUnavailable
	}
	return result, nil
}

// Validate is separate from transport so an injected test verifier cannot
// accidentally bypass production hostname/action checks in the request handler.
func Validate(v Verification, hostname string) error {
	if !v.Success {
		for _, code := range v.Codes {
			if code == "timeout-or-duplicate" {
				return ErrChallengeExpired
			}
			if code == "internal-error" || code == "invalid-input-secret" || code == "missing-input-secret" {
				return ErrChallengeUnavailable
			}
		}
		return ErrChallenge
	}
	if !strings.EqualFold(v.Hostname, hostname) || v.Action != ChallengeAction {
		return ErrChallenge
	}
	return nil
}
