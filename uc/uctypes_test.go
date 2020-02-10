package uc

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"testing"
)

func loadBytes(t *testing.T, filename string) []byte {
	filebytes, err := ioutil.ReadFile(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatal(err)
	}
	return filebytes
}

func getCTPtr(s string) *CalibreTime {
	ct := CalibreTime(s)
	return &ct
}

func TestMetaUnmarshal(t *testing.T) {
	meta := CalibreBookMeta{}
	testMetaStr := loadBytes(t, "timestamps.json")
	if err := json.Unmarshal(testMetaStr, &meta); err != nil {
		t.Errorf("Error umarshalling JSON: %v", err)
	}
	if meta.LastModified.GetTime() != nil || meta.Pubdate.GetTime() != nil || meta.Timestamp.GetTime() == nil {
		t.Errorf("Expected: <nil>, <nil>, time.Time\nGot: %v, %v, %v", meta.LastModified, meta.Pubdate, meta.Timestamp)
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		name   string
		ts     string
		result *CalibreTime
	}{
		{name: "RFC3339 UTC TS", ts: "2020-02-10T22:40:38+00:00", result: getCTPtr("2020-02-10T22:40:38+00:00")},
		{name: "RFC3339 Zulu TS", ts: "2020-02-10T22:40:38Z", result: getCTPtr("2020-02-10T22:40:38Z")},
		{name: "RFC3339 NZDT TS", ts: "2020-02-11T11:40:38+13:00", result: getCTPtr("2020-02-11T11:40:38+13:00")},
		{name: "Non-RFC3339 TS", ts: "11/02/2020 11:40AM", result: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseTime(tt.ts)
			if (got != nil && tt.result != nil) && (*got != *tt.result) {
				t.Errorf("Got: %v, expected %v", got, tt.result)
			} else if got != nil && tt.result == nil {
				t.Errorf("Got: %v, expected %v", got, tt.result)
			}
		})
	}
}
