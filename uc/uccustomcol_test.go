package uc

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUserMetaUnmarshal(t *testing.T) {
	userMeta := make([]map[string]CalibreCustomColumn, 0)
	userMetaBytes := loadBytes(t, "usermeta.json")
	if err := json.Unmarshal(userMetaBytes, &userMeta); err != nil {
		t.Errorf("Error umarshalling JSON: %v", err)
	}
}

func TestConvertCalDTFormatStr(t *testing.T) {
	tests := []struct {
		name   string
		fmt    string
		result string
	}{
		{name: "Test 1", fmt: "hh:mm:ss ap dddd dd MMMM yyyy", result: "03:04:05 pm Monday 02 January 2006"},
		{name: "Test 2", fmt: "yyyy/M/d hh:m:s", result: "2006/1/2 15:4:5"},
		{name: "Test 3", fmt: "dd/MM/yy", result: "02/01/06"},
		{name: "iso", fmt: "iso", result: time.RFC3339},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCalDateTimeFmtStr(tt.fmt)
			if err != nil {
				t.Errorf("Error parsing '%s'", tt.fmt)
			}
			if got != tt.result {
				t.Errorf("Got: %s, expected %s", got, tt.result)
			}
		})
	}
}
