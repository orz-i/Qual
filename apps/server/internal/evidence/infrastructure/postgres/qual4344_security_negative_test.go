package postgres

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
)

// Security acceptance: a core archive member must have exactly one checked
// manifest entry. An attacker must not be able to omit a core member from the
// manifest, duplicate an unrelated entry to balance file counts, and thereby
// bypass SHA-256 verification of the omitted core member.
func TestQual4344AuditVersionOneCannotClaimThreadHistory(t *testing.T) {
	files := []archiveFile{
		{Name: "work_items.jsonl", Data: []byte("{}\n"), Rows: 1},
		{Name: "external_threads.jsonl", Data: []byte("{}\n"), Rows: 1},
		{Name: "work_thread_links.jsonl", Data: []byte("{}\n"), Rows: 1},
	}
	data, _, err := buildArchiveWithSchema(uuid.New(), time.Now(), files, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArchive(data); err == nil {
		t.Fatal("accepted v1 ZIP containing v2 Thread/Link facts under a misleading schema version")
	}
}

func TestQual4344AuditRejectsTamperedUnmanifestedCoreMember(t *testing.T) {
	original := []archiveFile{
		{Name: "work_items.jsonl", Data: []byte("{\"key\":\"QUAL-X\"}\n"), Rows: 1},
		{Name: "external_threads.jsonl", Data: []byte("{\"source\":\"github\"}\n"), Rows: 1},
		{Name: "work_thread_links.jsonl", Data: []byte("{\"thread\":\"1\"}\n"), Rows: 1},
		{Name: "agent_runs.jsonl", Data: []byte("{\"run\":\"original\"}\n"), Rows: 1},
		{Name: "agent_run_reports.jsonl", Data: []byte("{\"report\":\"original\"}\n"), Rows: 1},
	}
	cases := []struct {
		name    string
		version int
		omit    string
		files   []archiveFile
	}{
		{name: "v2 thread tampering", version: 2, omit: "external_threads.jsonl", files: original[:3]},
		{name: "v3 run tampering", version: 3, omit: "agent_runs.jsonl", files: original},
		{name: "v3 thread tampering", version: 3, omit: "work_thread_links.jsonl", files: original},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseline, _, err := buildArchiveWithSchema(uuid.New(), time.Now().UTC(), tc.files, tc.version)
			if err != nil {
				t.Fatal(err)
			}
			originalZip, err := zip.NewReader(bytes.NewReader(baseline), int64(len(baseline)))
			if err != nil {
				t.Fatal(err)
			}
			var forged bytes.Buffer
			writer := zip.NewWriter(&forged)
			for _, entry := range originalZip.File {
				reader, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(reader)
				_ = reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				if entry.Name == "manifest.json" {
					var m domain.Manifest
					if err := json.Unmarshal(data, &m); err != nil {
						t.Fatal(err)
					}
					var kept []domain.FileManifest
					var duplicate domain.FileManifest
					for _, file := range m.Files {
						if file.Path == "work_items.jsonl" {
							duplicate = file
						}
						if file.Path != tc.omit {
							kept = append(kept, file)
						}
					}
					kept = append(kept, duplicate)
					m.Files = kept
					data, err = json.Marshal(m)
					if err != nil {
						t.Fatal(err)
					}
				} else if entry.Name == tc.omit {
					data = []byte("{\"tampered\":true}\n")
				}
				out, err := writer.Create(entry.Name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := out.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyArchive(forged.Bytes()); err == nil {
				t.Fatalf("accepted tampered core file %q without its manifest SHA; duplicate manifest path hid missing coverage", tc.omit)
			}
		})
	}
}
