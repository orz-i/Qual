package mcpserver

import (
	"errors"
	"net/url"
	"strconv"
)

// The legacy no-argument tool remains v1; any explicit page selector opts in
// to the bounded, authorization-filtered REST Context v2 projection.
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
