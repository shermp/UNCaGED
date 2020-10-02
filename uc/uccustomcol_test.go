package uc

import (
	"encoding/json"
	"io/ioutil"
	"path"
	"testing"
	"time"
)

func loadCustomColTestData(fn string) ([]map[string]CalibreCustomColumn, error) {
	data := make([]map[string]CalibreCustomColumn, 0)
	b, err := ioutil.ReadFile(path.Join("testdata", fn))
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func TestUserMetaUnmarshal(t *testing.T) {
	if _, err := loadCustomColTestData("usermeta.json"); err != nil {
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

type customColTT struct {
	name    string
	colName string
	index   int
	strRes  string
	ctxRes  string
}

func commonCustomColTest(t *testing.T, tests []customColTT) {
	data, err := loadCustomColTestData("usermeta.json")
	if err != nil {
		t.Errorf("Failed to load usermeta.json")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, exists := data[tt.index][tt.colName]; !exists {
				t.Errorf("Column name %s doesn't exist in test data", tt.colName)
			}
			cc := data[tt.index][tt.colName]
			strRes := cc.String()
			if strRes != tt.strRes {
				t.Errorf("Got: %s, expected %s", strRes, tt.strRes)
			}
			ctxRes := cc.ContextualString()
			if ctxRes != tt.ctxRes {
				t.Errorf("Got: %s, expected %s", ctxRes, tt.ctxRes)
			}
		})
	}
}

func TestIntCol(t *testing.T) {
	cc := []customColTT{
		{name: "int_null", colName: "#intnum", index: 0, strRes: "", ctxRes: ""},
		{name: "int_2", colName: "#intnum", index: 1, strRes: "2", ctxRes: "2"},
	}
	commonCustomColTest(t, cc)
}

func TestSeriesCol(t *testing.T) {
	cc := []customColTT{
		{name: "series_null", colName: "#seriestext", index: 0, strRes: "", ctxRes: ""},
		{name: "series_series", colName: "#seriestext", index: 1, strRes: "series", ctxRes: "series [1]"},
	}
	commonCustomColTest(t, cc)
}

func TestBoolCol(t *testing.T) {
	cc := []customColTT{
		{name: "bool_null", colName: "#yesno", index: 0, strRes: "", ctxRes: ""},
		{name: "bool_true", colName: "#yesno", index: 1, strRes: "true", ctxRes: "true"},
	}
	commonCustomColTest(t, cc)
}

func TestTextCol(t *testing.T) {
	cc := []customColTT{
		{name: "std_text_null", colName: "#stdtext", index: 0, strRes: "", ctxRes: ""},
		{name: "std_text_default", colName: "#stdtext", index: 1, strRes: "\"default\"", ctxRes: "\"default\""},
	}
	commonCustomColTest(t, cc)
}

func TestCompositeTagsCol(t *testing.T) {
	cc := []customColTT{
		{name: "com_col", colName: "#compcoltags", index: 0, strRes: "AuthorTitle", ctxRes: "AuthorTitle"},
		{name: "com_col2", colName: "#compcoltags", index: 1, strRes: "AuthorTitle", ctxRes: "AuthorTitle"},
	}
	commonCustomColTest(t, cc)
}

func TestRatingCol(t *testing.T) {
	cc := []customColTT{
		{name: "rating_null", colName: "#ratingfield", index: 0, strRes: "", ctxRes: ""},
		{name: "rating_7", colName: "#ratingfield", index: 1, strRes: "7", ctxRes: "★★★½"},
	}
	commonCustomColTest(t, cc)
}

func TestLongTextMarkdownCol(t *testing.T) {
	cc := []customColTT{
		{name: "longtext_md_null", colName: "#longtextmd", index: 0, strRes: "", ctxRes: ""},
		{name: "longtext_md", colName: "#longtextmd", index: 1, strRes: "*Testing*", ctxRes: "*Testing*"},
	}
	commonCustomColTest(t, cc)
}

func TestFixedSetTextCol(t *testing.T) {
	cc := []customColTT{
		{name: "fixed_text_null", colName: "#fixedsettext", index: 0, strRes: "", ctxRes: ""},
		{name: "fixed_text_one", colName: "#fixedsettext", index: 1, strRes: "one", ctxRes: "one"},
	}
	commonCustomColTest(t, cc)
}

func TestLongTextHTMLCol(t *testing.T) {
	cc := []customColTT{
		{name: "longtext_html_null", colName: "#longtexthtml", index: 0, strRes: "", ctxRes: ""},
		{name: "longtext_html_content1", colName: "#longtexthtml", index: 1, strRes: "<div>\n<p>This is some <span style=\"font-style: italic; font-weight: 600\">html</span> text</p></div>", ctxRes: "<div>\n<p>This is some <span style=\"font-style: italic; font-weight: 600\">html</span> text</p></div>"},
	}
	commonCustomColTest(t, cc)
}

func TestCSVCol(t *testing.T) {
	cc := []customColTT{
		{name: "csvtext_null", colName: "#csvtext", index: 0, strRes: "", ctxRes: ""},
		{name: "csvtext_onetwo", colName: "#csvtext", index: 1, strRes: "one,two", ctxRes: "one, two"},
	}
	commonCustomColTest(t, cc)
}

func TestDateCol(t *testing.T) {
	cc := []customColTT{
		{name: "date_null", colName: "#datefield", index: 0, strRes: "", ctxRes: ""},
		{name: "date_1", colName: "#datefield", index: 1, strRes: "2020-09-15T23:07:40.938000+00:00", ctxRes: "2020-09-15T23:07:40Z"},
	}
	commonCustomColTest(t, cc)
}

func TestFloatCol(t *testing.T) {
	cc := []customColTT{
		{name: "float_null", colName: "#floatnum", index: 0, strRes: "", ctxRes: ""},
		{name: "float_2.0", colName: "#floatnum", index: 1, strRes: "2", ctxRes: "2"},
	}
	commonCustomColTest(t, cc)
}
