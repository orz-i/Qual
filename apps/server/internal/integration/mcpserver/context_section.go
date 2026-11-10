package mcpserver

import (
	"errors"
	"net/url"
	"strconv"
)

// Both omitted and explicit section requests use the same bounded,
// authorization-filtered REST Context v2. Empty section means overview.
func contextSectionQuery(section string, limit int, cursor string) (url.Values, error) {
	if section == "" {
		section = "overview"
	}
	switch section {
	case "overview", "dependencies", "comments", "reviews", "acceptance",
		"coverage", "attempts", "artifacts", "references":
	default:
		return nil, errors.New("VALIDATION_ERROR")
	}
	if limit < 0 || limit > 100 || len(cursor) > 512 ||
		(cursor != "" && (section == "overview" || section == "acceptance")) {
		return nil, errors.New("VALIDATION_ERROR")
	}
	q := url.Values{}
	q.Set("section", section)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q, nil
}
