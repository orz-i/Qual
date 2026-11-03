package domain

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

const MaxJUnitXMLBytes = 1 << 20

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}
type junitCase struct {
	Name       string          `xml:"name,attr"`
	ClassName  string          `xml:"classname,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Failure    *struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	} `xml:"failure"`
	Error *struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	} `xml:"error"`
	Skipped *struct{} `xml:"skipped"`
}
type junitSuite struct {
	Cases  []junitCase  `xml:"testcase"`
	Suites []junitSuite `xml:"testsuite"`
}
type junitReport struct {
	XMLName xml.Name
	Cases   []junitCase  `xml:"testcase"`
	Suites  []junitSuite `xml:"testsuite"`
}
type ReportCase struct {
	CaseKey string
	Status  ResultStatus
	Note    string
}

// ParseJUnit accepts JUnit-style reports from external runners. The case key
// is explicit; Qual never invents a TestCase based on an unknown suite name.
func ParseJUnit(data []byte) ([]ReportCase, error) {
	if len(data) < 1 || len(data) > MaxJUnitXMLBytes {
		return nil, ErrValidation
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	// DTD/DOCTYPE is never required to report test results.
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrValidation
		}
		if dir, ok := tok.(xml.Directive); ok && strings.Contains(strings.ToUpper(string(dir)), "DOCTYPE") {
			return nil, ErrValidation
		}
	}
	var report junitReport
	if err := xml.Unmarshal(data, &report); err != nil {
		return nil, ErrValidation
	}
	if report.XMLName.Local != "testsuite" && report.XMLName.Local != "testsuites" {
		return nil, ErrValidation
	}
	result := make([]ReportCase, 0)
	seen := make(map[string]bool)
	var addCase func(junitCase) error
	addCase = func(c junitCase) error {
		key := ""
		for _, property := range c.Properties {
			if property.Name == "qual.case_key" {
				if key != "" {
					return ErrValidation
				}
				key = property.Value
			}
		}
		if key == "" {
			key = c.Name
		}
		if key == "" || len(key) > 120 || seen[key] || len(result) >= 500 {
			return ErrValidation
		}
		seen[key] = true
		item := ReportCase{CaseKey: key, Status: Passed}
		if c.Failure != nil {
			item.Status = Failed
			item.Note = c.Failure.Message
		}
		if c.Error != nil {
			item.Status = Failed
			item.Note = c.Error.Message
		}
		if c.Skipped != nil {
			if c.Failure != nil || c.Error != nil {
				return ErrValidation
			}
			item.Status = Skipped
		}
		if len(item.Note) > 20000 {
			return ErrValidation
		}
		result = append(result, item)
		return nil
	}
	var visit func(junitSuite) error
	visit = func(s junitSuite) error {
		for _, c := range s.Cases {
			if err := addCase(c); err != nil {
				return err
			}
		}
		for _, child := range s.Suites {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, c := range report.Cases {
		if err := addCase(c); err != nil {
			return nil, err
		}
	}
	for _, s := range report.Suites {
		if err := visit(s); err != nil {
			return nil, err
		}
	}
	if len(result) == 0 {
		return nil, ErrValidation
	}
	return result, nil
}
