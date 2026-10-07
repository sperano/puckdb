package mcpserver

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
)

// ResultJSON marshals a single value as JSON and returns it as a text tool result.
func ResultJSON(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}
	return mcp.NewToolResultText(string(data)), nil
}

// noResultsText is what a tool returns, or a CSV section holds, for no rows.
const noResultsText = "no results"

// ResultCSV formats a slice of structs as CSV text, using json struct tags as headers.
// Returns a text tool result. Returns "no results" if the slice is empty.
func ResultCSV(items any) (*mcp.CallToolResult, error) {
	text, err := csvText(items)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(text), nil
}

// csvText formats a slice of structs as CSV, using json struct tags as
// headers, or noResultsText when the slice is empty.
func csvText(items any) (string, error) {
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		return "", fmt.Errorf("CSV rows must be a slice, got %T", items)
	}
	if rv.Len() == 0 {
		return noResultsText, nil
	}

	elemType := rv.Type().Elem()
	// Dereference pointer element types.
	if elemType.Kind() == reflect.Ptr {
		elemType = elemType.Elem()
	}

	info := cachedTypeInfo(elemType)

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Write header row.
	if err := w.Write(info.headers); err != nil {
		return "", fmt.Errorf("write CSV header: %w", err)
	}

	// Write data rows.
	row := make([]string, len(info.indices))
	for i := range rv.Len() {
		elem := rv.Index(i)
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		for j, idx := range info.indices {
			row[j] = formatField(elem.Field(idx))
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("write CSV row %d: %w", i, err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("flush CSV: %w", err)
	}
	return buf.String(), nil
}

const (
	// commentPrefix starts the metadata header and section titles of a
	// multi-section result; CSV readers skip such lines as comments.
	commentPrefix = "# "
	// headerQuotedChars force a header value to be Go-quoted, so the line
	// still splits into key=value fields on spaces.
	headerQuotedChars = " \t\r\n\"=#"
)

// metadataHeader is the one "# key=value key=value" line a tool prints
// before its CSV sections, so per-result metadata is not repeated on every
// row.
type metadataHeader struct {
	fields []string
}

// add appends key=value; an empty value or one holding a space, quote, = or
// # is Go-quoted.
func (h *metadataHeader) add(key string, value any) {
	s := fmt.Sprint(value)
	if s == "" || strings.ContainsAny(s, headerQuotedChars) {
		s = strconv.Quote(s)
	}
	h.fields = append(h.fields, key+"="+s)
}

func (h metadataHeader) String() string {
	return commentPrefix + strings.Join(h.fields, " ")
}

// csvSection formats rows as CSV under a "# title" line.
func csvSection(title string, rows any) (string, error) {
	text, err := csvText(rows)
	if err != nil {
		return "", fmt.Errorf("%s: %w", title, err)
	}
	return commentPrefix + title + "\n" + strings.TrimSuffix(text, "\n") + "\n", nil
}

// typeInfo caches header names and field indices for a struct type.
type typeInfo struct {
	headers []string
	indices []int
}

var typeInfoCache sync.Map // reflect.Type → *typeInfo

func cachedTypeInfo(t reflect.Type) *typeInfo {
	if v, ok := typeInfoCache.Load(t); ok {
		return v.(*typeInfo)
	}

	var headers []string
	var indices []int
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" etc.
		if idx := len(tag); idx > 0 {
			for k, c := range tag {
				if c == ',' {
					tag = tag[:k]
					break
				}
			}
		}
		headers = append(headers, tag)
		indices = append(indices, i)
	}

	info := &typeInfo{headers: headers, indices: indices}
	typeInfoCache.Store(t, info)
	return info
}

// formatField converts a struct field value to a string, handling pgtype wrappers.
func formatField(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}

	iface := v.Interface()

	// Handle pgtype nullable types.
	switch val := iface.(type) {
	case pgtype.Text:
		if !val.Valid {
			return ""
		}
		return val.String
	case pgtype.Int2:
		if !val.Valid {
			return ""
		}
		return fmt.Sprintf("%d", val.Int16)
	case pgtype.Int4:
		if !val.Valid {
			return ""
		}
		return fmt.Sprintf("%d", val.Int32)
	case pgtype.Int8:
		if !val.Valid {
			return ""
		}
		return fmt.Sprintf("%d", val.Int64)
	case pgtype.Float4:
		if !val.Valid {
			return ""
		}
		return fmt.Sprintf("%g", val.Float32)
	case pgtype.Float8:
		if !val.Valid {
			return ""
		}
		return fmt.Sprintf("%g", val.Float64)
	case pgtype.Bool:
		if !val.Valid {
			return ""
		}
		if val.Bool {
			return "true"
		}
		return "false"
	case pgtype.Date:
		if !val.Valid {
			return ""
		}
		return val.Time.Format("2006-01-02")
	case pgtype.Timestamptz:
		if !val.Valid {
			return ""
		}
		return val.Time.Format(time.RFC3339)
	case pgtype.Numeric:
		if !val.Valid {
			return ""
		}
		f, _ := val.Float64Value()
		return fmt.Sprintf("%g", f.Float64)
	}

	// Handle sqlc nullable enums (Null* structs with {EnumField, Valid bool} pattern).
	if v.Kind() == reflect.Struct && v.NumField() == 2 {
		validField := v.FieldByName("Valid")
		if validField.IsValid() && validField.Kind() == reflect.Bool {
			if !validField.Bool() {
				return ""
			}
			enumField := v.Field(0)
			return fmt.Sprintf("%v", enumField.Interface())
		}
	}

	// Handle standard types.
	switch val := iface.(type) {
	case time.Time:
		if val.IsZero() {
			return ""
		}
		return val.Format(time.RFC3339)
	case *string:
		if val == nil {
			return ""
		}
		return *val
	case *int32:
		if val == nil {
			return ""
		}
		return fmt.Sprintf("%d", *val)
	case *int64:
		if val == nil {
			return ""
		}
		return fmt.Sprintf("%d", *val)
	}

	return fmt.Sprintf("%v", iface)
}
