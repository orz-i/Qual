package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSpecificationChecksumAndValidation(t *testing.T) {
	s := Specification{Title: " Log in ", Steps: []Step{{Action: "Open login", Expected: "Login shown"}}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	x, err := s.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	y, err := s.Normalize().Checksum()
	if err != nil || x != y {
		t.Fatal("normalized digest mismatch")
	}
	bad := []Specification{{Title: ""}, {Title: "Test", Steps: []Step{{Action: ""}}}, {Title: strings.Repeat("a", 241)}}
	for _, candidate := range bad {
		if !errors.Is(candidate.Validate(), ErrValidation) {
			t.Errorf("accepted invalid case: %#v", candidate)
		}
	}
}
func TestJUnitParser(t *testing.T) {
	cases := []struct {
		xml      string
		statuses []ResultStatus
	}{
		{`<testsuite name="suite"><testcase name="ENG-TC-1"/><testcase name="other"><properties><property name="qual.case_key" value="ENG-TC-2"/></properties><failure message="wrong"/></testcase><testcase name="ENG-TC-3"><skipped/></testcase></testsuite>`, []ResultStatus{Passed, Failed, Skipped}},
		{`<testsuites><testsuite><testcase name="ENG-TC-1"><error message="crash"/></testcase></testsuite></testsuites>`, []ResultStatus{Failed}},
	}
	for _, tt := range cases {
		got, err := ParseJUnit([]byte(tt.xml))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tt.statuses) {
			t.Fatalf("expected %d, got %d", len(tt.statuses), len(got))
		}
		for i := range got {
			if got[i].Status != tt.statuses[i] {
				t.Errorf("unexpected result: %s", got[i].Status)
			}
		}
	}
	bad := []string{
		`<!DOCTYPE testsuite [<!ENTITY x SYSTEM "file:///etc/passwd">]><testsuite><testcase name="ENG-TC-1"/></testsuite>`,
		`<testsuite><testcase name="ENG-TC-1"/><testcase name="ENG-TC-1"/></testsuite>`,
		`<testsuite></testsuite>`, `<not-junit><testcase name="ENG-TC-1"/></not-junit>`,
		`<testsuite><testcase name="ENG-TC-1"><skipped/><failure/></testcase></testsuite>`,
	}
	for _, value := range bad {
		if _, err := ParseJUnit([]byte(value)); !errors.Is(err, ErrValidation) {
			t.Errorf("accepted invalid XML: %q: %v", value, err)
		}
	}
	if _, err := ParseJUnit(bytes.Repeat([]byte("a"), MaxJUnitXMLBytes+1)); !errors.Is(err, ErrValidation) {
		t.Fatal("oversized report was accepted")
	}
}
