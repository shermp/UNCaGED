package uc

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/slongfield/pyfmt"
)

// CalibreCustomColumn contains metadata about a single custom column
type CalibreCustomColumn struct {
	Value        interface{}          `json:"#value#"`
	ColNum       int                  `json:"colnum"`
	RecIndex     int                  `json:"rec_index"`
	Label        string               `json:"label"`
	Extra        interface{}          `json:"#extra#"`
	Datatype     CalCustomColDataType `json:"datatype"`
	Name         string               `json:"name"`
	CategorySort string               `json:"category_sort"`
	IsCsp        bool                 `json:"is_csp"`
	Kind         string               `json:"kind"`
	IsCustom     bool                 `json:"is_custom"`
	IsEditable   bool                 `json:"is_editable"`
	Column       string               `json:"column"`
	IsMultiple2  struct {
		UIToList    string `json:"ui_to_list,omitempty"`
		CacheToList string `json:"cache_to_list,omitempty"`
		ListToUI    string `json:"list_to_ui,omitempty"`
	} `json:"is_multiple2"`
	IsMultiple  *string         `json:"is_multiple"`
	SearchTerms []string        `json:"search_terms"`
	IsCategory  bool            `json:"is_category"`
	Table       string          `json:"table"`
	Display     json.RawMessage `json:"display"`
	LinkColumn  string          `json:"link_column"`
}

// CalCustomColDataType is the data type the column holds
type CalCustomColDataType string

// KnownType checks whether the data type is known to UC
func (t *CalCustomColDataType) KnownType() bool {
	switch *t {
	case "int",
		"series",
		"bool",
		"text",
		"composite",
		"rating",
		"comments",
		"enumeration",
		"datetime",
		"float":
		return true
	}
	return false
}

// CalCustomColDisplay is the generic display type for custom columns
type CalCustomColDisplay struct {
	Description string `json:"description"`
}

// CalCustomColDisplayNum is the display type for int and float custom columns
type CalCustomColDisplayNum struct {
	Description  string  `json:"description"`
	NumberFormat *string `json:"number_format"`
}

// CalCustomColDisplayText is the display type for text custom columns
type CalCustomColDisplayText struct {
	DefaultValue   string `json:"default_value"`
	Description    string `json:"description"`
	UseDecorations int    `json:"use_decorations,omitempty"`
	IsNames        bool   `json:"is_names,omitempty"`
}

// CalCustomColDisplayComposite is the display type for composite custom columns
type CalCustomColDisplayComposite struct {
	ContainsHTML      bool   `json:"contains_html"`
	MakeCategory      bool   `json:"make_category"`
	CompositeTemplate string `json:"composite_template"`
	CompositeSort     string `json:"composite_sort"`
	Description       string `json:"description"`
	UseDecorations    int    `json:"use_decorations,omitempty"`
}

// CalCustomColDisplayRating is the display type for ratings custom columns
type CalCustomColDisplayRating struct {
	Description    string `json:"description"`
	AllowHalfStars bool   `json:"allow_half_stars"`
}

// CalCustomColDisplayComments is the display type for long form text columns
type CalCustomColDisplayComments struct {
	DefaultValue    string `json:"default_value"`
	InterpretAs     string `json:"interpret_as"`
	Description     string `json:"description"`
	HeadingPosition string `json:"heading_position"`
}

// CalCustomColDisplayEnum is the display type for enumerated custom columns
type CalCustomColDisplayEnum struct {
	EnumValues     []string `json:"enum_values"`
	Description    string   `json:"description"`
	UseDecorations int      `json:"use_decorations"`
	EnumColors     []string `json:"enum_colors"`
}

// CalCustomColDisplayDateTime is the display type for datetime custom columns
type CalCustomColDisplayDateTime struct {
	Description string  `json:"description"`
	DateFormat  *string `json:"date_format"`
}

func parseNextFmt(fmt string, use24 bool) (string, int) {
	if strings.HasPrefix(fmt, "dddd") {
		return "Monday", 3
	} else if strings.HasPrefix(fmt, "ddd") {
		return "Mon", 2
	} else if strings.HasPrefix(fmt, "dd") {
		return "02", 1
	} else if strings.HasPrefix(fmt, "d") {
		return "2", 0
	} else if strings.HasPrefix(fmt, "MMMM") {
		return "January", 3
	} else if strings.HasPrefix(fmt, "MMM") {
		return "Jan", 2
	} else if strings.HasPrefix(fmt, "MM") {
		return "01", 1
	} else if strings.HasPrefix(fmt, "M") {
		return "1", 0
	} else if strings.HasPrefix(fmt, "yyyy") {
		return "2006", 3
	} else if strings.HasPrefix(fmt, "yy") {
		return "06", 1
	} else if strings.HasPrefix(fmt, "hh") {
		if use24 {
			return "15", 1
		}
		return "03", 1
	} else if strings.HasPrefix(fmt, "h") {
		if use24 {
			return "15", 0
		}
		return "3", 0
	} else if strings.HasPrefix(fmt, "mm") {
		return "04", 1
	} else if strings.HasPrefix(fmt, "m") {
		return "4", 0
	} else if strings.HasPrefix(fmt, "ss") {
		return "05", 1
	} else if strings.HasPrefix(fmt, "s") {
		return "5", 0
	} else if strings.HasPrefix(fmt, "ap") {
		return "pm", 1
	} else if strings.HasPrefix(fmt, "AP") {
		return "PM", 1
	}
	return "", 0
}

func parseCalDateTimeFmtStr(calFmt string) (string, error) {
	if calFmt == "iso" {
		return time.RFC3339, nil
	}
	var skip = 0
	var s string
	var use24 = !(strings.Contains(calFmt, "ap") || strings.Contains(calFmt, "AP"))
	sb := strings.Builder{}
	for i, r := range calFmt {
		if skip > 0 {
			skip--
			continue
		}
		switch r {
		case 'd', 'M', 'y', 'h', 'm', 's', 'a', 'A':
			s, skip = parseNextFmt(calFmt[i:], use24)
			sb.WriteString(s)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String(), nil
}

func formatCalFloat(calFmt *string, num float64) string {
	if calFmt != nil {
		if str, err := pyfmt.Fmt(*calFmt, num); err == nil {
			return str
		}
	}
	return strconv.FormatFloat(num, 'f', -1, 64)
}

func formatCalInt(calFmt *string, num int) string {
	if calFmt != nil {
		if str, err := pyfmt.Fmt(*calFmt, num); err == nil {
			return str
		}
	}
	return strconv.Itoa(num)
}

func formatRating(rating int, allowHalf bool) string {
	// Rating is a number from 0 - 10, with 0 being no stars, and 10 being half stars
	if rating > 10 {
		return strings.Repeat("★", 5)
	}
	quot := rating / 2
	rem := rating % 2
	stars := strings.Repeat("★", quot)
	if rem > 0 && allowHalf {
		// Use the '1/2' codepoint, because half-stars weren't introduced
		// until unicode 11
		stars += "½"
	}
	return stars
}

// String returns the raw string representation of
// a value. The string is not formatted in any manner
func (u *CalibreCustomColumn) String() string {
	if u.Value == nil || !u.Datatype.KnownType() {
		return ""
	}
	switch u.Datatype {
	case "text":
		if u.IsMultiple != nil {
			if v, ok := u.Value.([]string); ok {
				return strings.Join(v, ",")
			}
			return ""
		}
		return u.Value.(string)
	case "comments", "series", "enumeration", "datetime", "composite":
		return u.Value.(string)
	case "float":
		return strconv.FormatFloat(u.Value.(float64), 'f', -1, 64)
	case "int", "rating":
		return strconv.Itoa(int(u.Value.(float64)))
	case "bool":
		strconv.FormatBool(u.Value.(bool))
	}
	return ""
}

// ContextualString generates a contextualised string based
// on the data type, and the display hints provided
func (u *CalibreCustomColumn) ContextualString() string {
	if u.Value == nil || !u.Datatype.KnownType() {
		return ""
	}
	switch u.Datatype {
	case "bool", "text", "comments", "series", "enumeration", "composite":
		return u.String()
	case "int", "float":
		var numFmt *string
		display := CalCustomColDisplayNum{}
		if err := json.Unmarshal(u.Display, &display); err == nil {
			numFmt = display.NumberFormat
		}
		if u.Datatype == "int" {
			return formatCalInt(numFmt, int(u.Value.(float64)))
		}
		return formatCalFloat(numFmt, u.Value.(float64))
	case "rating":
		rating := int(u.Value.(float64))
		return formatRating(rating, true)
	case "datetime":
		ct := CalibreTime(u.Value.(string))
		dt := ct.GetTime()
		if dt == nil {
			return u.Value.(string)
		}
		display := CalCustomColDisplayDateTime{}
		var dtFmt *string
		if err := json.Unmarshal(u.Display, &display); err == nil {
			dtFmt = display.DateFormat
		}
		if dtFmt != nil {
			if fmt, err := parseCalDateTimeFmtStr(*dtFmt); err == nil {
				return dt.Format(fmt)
			}
		}
		return dt.Format(time.RFC3339)
	}
	return ""
}
