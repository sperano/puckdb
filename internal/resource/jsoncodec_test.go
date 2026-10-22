package resource

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type jsonCodecFixture struct {
	Name string `json:"name"`
}

func TestParsePointer_Success(t *testing.T) {
	got, err := parsePointer[jsonCodecFixture]([]byte(`{"name":"sidney"}`), "parse fixture")
	if err != nil {
		t.Fatalf("parsePointer() unexpected error: %v", err)
	}
	if got == nil || got.Name != "sidney" {
		t.Fatalf("parsePointer() = %+v, want Name %q", got, "sidney")
	}
}

// On a decode error, parsePointer must return nil rather than a pointer to
// a partially decoded value: json.Unmarshal can populate some fields (and
// allocate the pointee) before it hits the error, so callers that only
// check err would otherwise be handed a misleadingly non-nil result.
func TestParsePointer_ErrorReturnsNilNotPartialValue(t *testing.T) {
	got, err := parsePointer[jsonCodecFixture]([]byte(`{"name":`), "parse fixture")
	if err == nil {
		t.Fatal("parsePointer() expected error, got nil")
	}
	if got != nil {
		t.Fatalf("parsePointer() on error = %+v, want nil", got)
	}
	if !strings.HasPrefix(err.Error(), "parse fixture: ") {
		t.Errorf("parsePointer() error = %q, want prefix %q", err.Error(), "parse fixture: ")
	}
}

// The wrapped decode error must stay reachable via errors.Unwrap/errors.Is,
// so a caller that distinguishes error causes (e.g. a syntax error vs a
// type mismatch) still can after the shared helper wraps it.
func TestParsePointer_WrapsUnderlyingErrorForUnwrap(t *testing.T) {
	_, err := parsePointer[jsonCodecFixture]([]byte(`not json`), "parse fixture")
	if err == nil {
		t.Fatal("parsePointer() expected error, got nil")
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("parsePointer() error %q does not unwrap to a *json.SyntaxError", err)
	}
}

func TestParseValue_Success(t *testing.T) {
	got, err := parseValue[[]jsonCodecFixture]([]byte(`[{"name":"sidney"}]`), "parse fixtures")
	if err != nil {
		t.Fatalf("parseValue() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "sidney" {
		t.Fatalf("parseValue() = %+v, want one fixture named %q", got, "sidney")
	}
}

// On a decode error, parseValue must return the zero value of T (nil for a
// slice), matching what every hand-written Parse returned before this
// helper replaced them, rather than a partially decoded value.
func TestParseValue_ErrorReturnsZeroValue(t *testing.T) {
	got, err := parseValue[[]jsonCodecFixture]([]byte(`{bad}`), "parse fixtures")
	if err == nil {
		t.Fatal("parseValue() expected error, got nil")
	}
	if got != nil {
		t.Fatalf("parseValue() on error = %+v, want nil slice", got)
	}
	if !strings.HasPrefix(err.Error(), "parse fixtures: ") {
		t.Errorf("parseValue() error = %q, want prefix %q", err.Error(), "parse fixtures: ")
	}
}

// json.Unmarshal decodes a slice element by element, so a type mismatch in
// a later element leaves earlier, successfully decoded elements sitting in
// the destination slice (verified directly: unmarshaling
// `[{"name":"sidney"},{"name":5}]` into a []jsonCodecFixture yields a
// non-nil, two-element slice alongside the error). parseValue must discard
// that partially decoded slice and return the zero value instead of
// handing the caller back something that looks like a decoded result.
func TestParseValue_DiscardsPartiallyDecodedSliceOnLaterError(t *testing.T) {
	got, err := parseValue[[]jsonCodecFixture]([]byte(`[{"name":"sidney"},{"name":5}]`), "parse fixtures")
	if err == nil {
		t.Fatal("parseValue() expected error, got nil")
	}
	if got != nil {
		t.Fatalf("parseValue() on error = %+v, want nil slice, not the partially decoded elements", got)
	}
}
