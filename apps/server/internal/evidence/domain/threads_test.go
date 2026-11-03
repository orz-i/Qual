package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestThreadValidation(t *testing.T) {
	valid := ThreadLinkInput{Provider: "github", ThreadKind: "pull_request", ExternalThreadID: "org/repo/pull/17", URL: "https://github.com/org/repo/pull/17", ExpectedVersion: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for _, value := range []string{
		"http://github.com/org/repo", "https://localhost/test",
		"https://127.0.0.1/test", "https://127.1/test", "https://[::1]/test", "https://example.local/post",
		"https://example.com/?token=secret", "https://example.com/#secret",
		"https://alice:password@example.com/path", "https://example.com:8888/post",
		"https://example.com./bad", "https://example.com/p\nath",
		"https://service.corp/path", "https://somehost.lan/",
		"https://hidden.onion/path", "https://internal.example/path",
		"https://sub.example.test/path", "https://co.uk/path",
		"https://example.com.evil.invalid/path",
	} {
		t.Run(value, func(t *testing.T) {
			in := valid
			in.URL = value
			if in.Validate() == nil {
				t.Fatalf("unsafe URL accepted: %q", value)
			}
		})
	}
	for _, raw := range []string{
		"https://example.com/path", "https://github.com:443/org/repo/pull/17",
		"https://example.co.uk/path", "https://team.github.io/thread",
	} {
		t.Run("valid URL "+raw, func(t *testing.T) {
			in := valid
			in.URL = raw
			if err := in.Validate(); err != nil {
				t.Fatalf("public URL rejected: %s (%v)", raw, err)
			}
		})
	}
	for _, tc := range []struct {
		provider, kind, externalID string
	}{
		{"github", "pull_request", "15"},
		{"github", "pull_request", "other/repo/15"},
		{"github", "pull_request", "org/repo/issues/15"},
		{"github", "pull_request", "org/repo/pull/0"},
		{"github", "pull_request", "org/repo/pull/0015"},
		{"github", "pull_request", "/repo/pull/15"},
		{"github", "issue", "15"},
		{"github", "issue", "org/repo/pull/15"},
		{"slack", "discussion", "message-42"},
		{"linear", "issue", "project//issue-42"},
		{"linear", "issue", "project/../issue-42"},
		{"linear", "issue", "project/issue?token=secret"},
		{"slack", "discussion", "workspace/channel/ hi"},
	} {
		t.Run("invalid source "+tc.provider+"/"+tc.externalID, func(t *testing.T) {
			in := valid
			in.Provider, in.ThreadKind, in.ExternalThreadID = tc.provider, tc.kind, tc.externalID
			if in.Validate() == nil {
				t.Fatalf("ambiguous source identity accepted: %+v", in)
			}
		})
	}
	for _, tc := range []struct {
		provider, kind, externalID string
	}{
		{"github", "issue", "org/repo/issues/200"},
		{"slack", "discussion", "workspace/channel/message-12"},
		{"linear", "issue", "project/issue-0"},
	} {
		in := valid
		in.Provider, in.ThreadKind, in.ExternalThreadID, in.URL = tc.provider, tc.kind, tc.externalID, ""
		if err := in.Validate(); err != nil {
			t.Fatalf("scoped source rejected: %+v: %v", in, err)
		}
	}
	in := valid
	in.Provider = "GitHub"
	if in.Validate() == nil {
		t.Fatal("non-canonical provider accepted")
	}
	in = valid
	in.ThreadKind = "A"
	if in.Validate() == nil {
		t.Fatal("non-canonical kind accepted")
	}
	in = valid
	in.ExternalThreadID = "org/repo/pull/17\x01"
	if in.Validate() == nil {
		t.Fatal("control character accepted")
	}
	in = valid
	in.URL = ""
	if in.Validate() != nil {
		t.Fatal("URL should be optional")
	}
	in.SourceReferenceID = new(uuid.UUID)
	if in.Validate() == nil {
		t.Fatal("nil UUID source reference accepted")
	}
}
