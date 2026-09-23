package media

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrContributor = errors.New("Use a name of up to 100 characters without control characters.")

// Contributor names are optional labels, never authenticated identities.
func NormalizeContributor(name string) (*string, error) {
	if !utf8.ValidString(name) {
		return nil, ErrContributor
	}
	name = strings.TrimSpace(name)
	for _, r := range name {
		if unicode.IsControl(r) {
			return nil, ErrContributor
		}
	}
	if utf8.RuneCountInString(name) > 100 {
		return nil, ErrContributor
	}
	if name == "" {
		return nil, nil
	}
	return &name, nil
}

type Contributor struct {
	Name       *string `json:"name"`
	PhotoCount int64   `json:"photo_count"`
}

// A bounded, admin-only summary; attribution is copied onto each asset so
// session expiry/cleanup cannot rewrite historical labels.
func (s *Service) Contributors(ctx context.Context, eventID int64) ([]Contributor, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT contributor_name,COUNT(*) FROM assets WHERE event_id=? AND status='ready' GROUP BY contributor_name ORDER BY COUNT(*) DESC, contributor_name LIMIT 100`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Contributor{}
	for rows.Next() {
		var c Contributor
		if err := rows.Scan(&c.Name, &c.PhotoCount); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
