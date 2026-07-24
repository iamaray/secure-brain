package query

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

func asset(id, name string, format domain.AssetFormat, state domain.AssetProcessingState, body string) Asset {
	return Asset{Asset: domain.Asset{
		ID: id, OriginalFilename: name, MediaType: "text/plain",
		Format: format, ProcessingState: state,
	}, Bytes: []byte(body)}
}

func errorCode(t *testing.T, err error) domain.Code {
	t.Helper()
	var appErr *domain.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected domain error, got %T: %v", err, err)
	}
	return appErr.Code
}

func TestExecuteCoreOperationsGolden(t *testing.T) {
	tests := []struct {
		name   string
		assets []Asset
		req    Request
		file   string
	}{
		{
			name: "raw read",
			assets: []Asset{
				asset("a", "a.txt", domain.AssetFormatText, domain.AssetStateReady, "A"),
				func() Asset {
					value := asset("b", "rows.csv", domain.AssetFormatCSV, domain.AssetStateReady, "x\n1\n")
					value.Asset.MediaType = "text/csv"
					return value
				}(),
			},
			req:  Request{Operation: OperationRawRead},
			file: "testdata/raw_read.golden.json",
		},
		{
			name: "text search",
			assets: []Asset{
				asset("a", "a.txt", domain.AssetFormatText, domain.AssetStateReady, "zero\nHit here\n"),
				asset("b", "blob.bin", domain.AssetFormatBinary, domain.AssetStateReady, "hit"),
			},
			req:  Request{Operation: OperationTextSearch, Query: "hit"},
			file: "testdata/text_search.golden.json",
		},
		{
			name: "CSV query",
			assets: []Asset{
				asset("scores", "scores.csv", domain.AssetFormatCSV, domain.AssetStateReady, "name,score\nAda,2\nBob,1\n"),
			},
			req: Request{
				Operation: OperationCSVQuery,
				Select:    []string{"name"},
				Filters:   []Filter{{Column: "score", Operator: ">", Value: "1"}},
				Limit:     10,
			},
			file: "testdata/csv_query.golden.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := Execute(tt.assets, tt.req, application.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(payload.Bytes)); got != strings.TrimSpace(string(want)) {
				t.Fatalf("golden payload mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestRawReadSinglePreservesOriginalPayload(t *testing.T) {
	in := asset("asset-1", "notes.md", domain.AssetFormatMarkdown, domain.AssetStateReady, "exact\x00bytes")
	in.Asset.MediaType = "text/markdown"
	payload, err := Execute([]Asset{in}, Request{Operation: OperationRawRead}, application.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload.Bytes) != string(in.Bytes) || payload.MediaType != "text/markdown" || payload.SuggestedFilename != "notes.md" {
		t.Fatalf("payload was not preserved: %#v", payload)
	}
	if len(in.Bytes) != 0 && &payload.Bytes[0] != &in.Bytes[0] {
		t.Fatal("single raw read unexpectedly copied the byte slice")
	}
}

func TestRawReadMultipleHasDeterministicGoldenManifest(t *testing.T) {
	a := asset("a", "a.txt", domain.AssetFormatText, domain.AssetStateReady, "A")
	b := asset("b", "b.bin", domain.AssetFormatBinary, domain.AssetStateReady, "\x00\xff")
	b.Asset.MediaType = "application/octet-stream"
	p1, err := Execute([]Asset{a, b}, Request{Operation: OperationRawRead}, application.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Execute([]Asset{a, b}, Request{Operation: OperationRawRead}, application.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if string(p1.Bytes) != string(p2.Bytes) {
		t.Fatal("manifest serialization is not deterministic")
	}
	sa := sha256.Sum256(a.Bytes)
	sb := sha256.Sum256(b.Bytes)
	want := `{"operation":"raw_read","assets":[` +
		`{"asset_id":"a","filename":"a.txt","media_type":"text/plain","byte_size":1,"sha256":"` + hex.EncodeToString(sa[:]) + `","data_base64":"QQ=="},` +
		`{"asset_id":"b","filename":"b.bin","media_type":"application/octet-stream","byte_size":2,"sha256":"` + hex.EncodeToString(sb[:]) + `","data_base64":"AP8="}]}`
	if got := string(p1.Bytes); got != want {
		t.Fatalf("manifest mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestTextSearchUnicodeFoldOrderContextAndSkip(t *testing.T) {
	assets := []Asset{
		asset("first", "one.txt", domain.AssetFormatText, domain.AssetStateReady, "no\nGreek final ς here\nFOUND"),
		asset("skip", "blob.bin", domain.AssetFormatBinary, domain.AssetStateReady, "Σ"),
		asset("second", "two.md", domain.AssetFormatMarkdown, domain.AssetStateReady, strings.Repeat("x", 220)+"Σ"),
	}
	payload, err := Execute(assets, Request{Operation: OperationTextSearch, Query: "Σ"}, application.Limits{MaxTextContextRunes: 20})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Matches []textMatch    `json:"matches"`
		Skipped []skippedAsset `json:"skipped"`
	}
	if err := json.Unmarshal(payload.Bytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Matches) != 2 || got.Matches[0].AssetID != "first" || got.Matches[0].LineNumber != 2 || got.Matches[1].AssetID != "second" {
		t.Fatalf("unexpected deterministic matches: %#v", got.Matches)
	}
	if len([]rune(got.Matches[1].Context)) != 20 || !strings.Contains(got.Matches[1].Context, "Σ") {
		t.Fatalf("context is not bounded around match: %q", got.Matches[1].Context)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].AssetID != "skip" {
		t.Fatalf("unexpected skipped metadata: %#v", got.Skipped)
	}
}

func TestTextSearchLimitsMatches(t *testing.T) {
	payload, err := Execute(
		[]Asset{
			asset("a", "a.txt", domain.AssetFormatText, domain.AssetStateReady, "hit\nhit\nhit"),
			asset("later-skip", "x.bin", domain.AssetFormatBinary, domain.AssetStateReady, "hit"),
		},
		Request{Operation: OperationTextSearch, Query: "hit"}, application.Limits{MaxTextMatches: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	var got textSearchResult
	if err := json.Unmarshal(payload.Bytes, &got); err != nil {
		t.Fatal(err)
	}
	if got.MatchCount != 2 || !got.Truncated {
		t.Fatalf("expected capped result, got %#v", got)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].AssetID != "later-skip" {
		t.Fatalf("later incompatible assets missing from skipped metadata: %#v", got.Skipped)
	}
}

func TestTextSearchRejectsInvalidAndIncompatibleRequests(t *testing.T) {
	tests := []struct {
		name   string
		assets []Asset
		query  string
	}{
		{"empty query", []Asset{asset("a", "a.txt", domain.AssetFormatText, domain.AssetStateReady, "x")}, ""},
		{"all incompatible", []Asset{asset("a", "a.csv", domain.AssetFormatCSV, domain.AssetStateReady, "x")}, "x"},
		{"parse failed", []Asset{asset("a", "a.txt", domain.AssetFormatText, domain.AssetStateParseFailed, "x")}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Execute(tt.assets, Request{Operation: OperationTextSearch, Query: tt.query}, application.Limits{})
			if errorCode(t, err) != domain.CodeQueryInvalid {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCSVQueryInferenceFilteringProjectionPagination(t *testing.T) {
	csvAsset := asset("scores", "scores.csv", domain.AssetFormatCSV, domain.AssetStateReady,
		"author,score,note\nAda,0.80,Foundation Model\nBob,.8,other\nCara,1.2,FOUNDATION work\nDan,,empty\n")
	csvAsset.Asset.MediaType = "text/csv"
	payload, err := Execute([]Asset{csvAsset}, Request{
		Operation: OperationCSVQuery,
		Select:    []string{"author", "score"},
		Filters: []Filter{
			{Column: "score", Operator: ">=", Value: "0.8"},
			{Column: "note", Operator: "contains", Value: "foundation"},
		},
		Limit: 1,
	}, application.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var got csvQueryResult
	if err := json.Unmarshal(payload.Bytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 {
		t.Fatalf("files = %#v", got.Files)
	}
	f := got.Files[0]
	if !reflect.DeepEqual(f.Columns, []string{"author", "score"}) || !reflect.DeepEqual(f.Rows, [][]string{{"Ada", "0.80"}}) {
		t.Fatalf("unexpected projection: %#v", f)
	}
	if f.MatchedRowCount != 2 || f.ReturnedRowCount != 1 || !f.HasMore {
		t.Fatalf("unexpected pagination: %#v", f)
	}
}

func TestCSVOperatorsAndInference(t *testing.T) {
	tests := []struct {
		name, cell, op, value string
		numeric               bool
		want                  bool
	}{
		{"numeric equality", ".80", "=", "0.8", true, true},
		{"numeric inequality", "2", "!=", "2.0", true, false},
		{"greater", "10", ">", "2", true, true},
		{"less", "1", "<", "2", true, true},
		{"greater equal", "2", ">=", "2", true, true},
		{"less equal", "2", "<=", "2", true, true},
		{"string case sensitive equality", "Ada", "=", "ada", false, false},
		{"string ordering", "b", ">", "A", false, true},
		{"unicode contains", "τελικό ς", "contains", "Σ", false, true},
		{"empty contains", "anything", "contains", "", false, true},
		{"empty numeric column cell stays lexical", "", "<", "0", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterMatches(tt.cell, tt.op, tt.value, tt.numeric); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	if !inferNumeric([][]string{{"1"}, {""}, {"2.5"}}, 0) {
		t.Fatal("finite non-empty values should infer numeric")
	}
	for name, rows := range map[string][][]string{
		"all empty": {{""}}, "non-numeric": {{"1"}, {"x"}}, "infinite": {{"Inf"}}, "nan": {{"NaN"}},
		"hexadecimal": {{"0x1p2"}}, "underscores": {{"1_000"}},
	} {
		t.Run(name, func(t *testing.T) {
			if inferNumeric(rows, 0) {
				t.Fatal("column unexpectedly inferred numeric")
			}
		})
	}
}

func TestCSVValidationAndBounds(t *testing.T) {
	a := asset("a", "a.csv", domain.AssetFormatCSV, domain.AssetStateReady, "name,value\na,1\nb,2\n")
	tests := []struct {
		name string
		req  Request
	}{
		{"unknown select", Request{Operation: OperationCSVQuery, Select: []string{"missing"}}},
		{"duplicate select", Request{Operation: OperationCSVQuery, Select: []string{"name", "name"}}},
		{"unknown filter", Request{Operation: OperationCSVQuery, Filters: []Filter{{Column: "missing", Operator: "=", Value: "x"}}}},
		{"unknown operator", Request{Operation: OperationCSVQuery, Filters: []Filter{{Column: "name", Operator: "LIKE", Value: "x"}}}},
		{"negative limit", Request{Operation: OperationCSVQuery, Limit: -1}},
		{"negative offset", Request{Operation: OperationCSVQuery, Offset: -1}},
		{"large offset", Request{Operation: OperationCSVQuery, Offset: 10001}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Execute([]Asset{a}, tt.req, application.Limits{})
			if errorCode(t, err) != domain.CodeQueryInvalid {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	payload, err := Execute([]Asset{a}, Request{Operation: OperationCSVQuery, Limit: 100}, application.Limits{MaxCSVRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got csvQueryResult
	if err := json.Unmarshal(payload.Bytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files[0].Rows) != 1 || !got.Files[0].HasMore {
		t.Fatalf("server limit was not applied: %#v", got.Files[0])
	}
}

func TestCSVRejectsMalformedAndAmbiguousHeaders(t *testing.T) {
	for name, body := range map[string]string{
		"no records":        "",
		"malformed quoting": "name\n\"unterminated",
		"empty header":      "name,\na,b\n",
		"duplicate header":  "name,name\na,b\n",
	} {
		t.Run(name, func(t *testing.T) {
			a := asset("a", "a.csv", domain.AssetFormatCSV, domain.AssetStateReady, body)
			_, err := Execute([]Asset{a}, Request{Operation: OperationCSVQuery}, application.Limits{})
			if errorCode(t, err) != domain.CodeQueryInvalid {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCSVSkipsIncompatibleButRejectsAllIncompatible(t *testing.T) {
	csvAsset := asset("csv", "a.csv", domain.AssetFormatCSV, domain.AssetStateReady, "x\ny\n")
	bin := asset("bin", "b.bin", domain.AssetFormatBinary, domain.AssetStateReady, "x")
	payload, err := Execute([]Asset{bin, csvAsset}, Request{Operation: OperationCSVQuery}, application.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var got csvQueryResult
	if err := json.Unmarshal(payload.Bytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].AssetID != "bin" || len(got.Files) != 1 {
		t.Fatalf("unexpected mixed compatibility result: %#v", got)
	}
	_, err = Execute([]Asset{bin}, Request{Operation: OperationCSVQuery}, application.Limits{})
	if errorCode(t, err) != domain.CodeQueryInvalid {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCSVLimitIsGlobalAcrossFiles(t *testing.T) {
	one := asset("one", "one.csv", domain.AssetFormatCSV, domain.AssetStateReady, "x\na\nb\n")
	two := asset("two", "two.csv", domain.AssetFormatCSV, domain.AssetStateReady, "x\nc\nd\n")
	payload, err := Execute([]Asset{one, two}, Request{Operation: OperationCSVQuery, Limit: 2}, application.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var got csvQueryResult
	if err := json.Unmarshal(payload.Bytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.Files[0].ReturnedRowCount != 2 || got.Files[1].ReturnedRowCount != 0 {
		t.Fatalf("global row budget not enforced: %#v", got.Files)
	}
	if !got.Files[1].HasMore {
		t.Fatalf("exhausted global budget should report remaining rows: %#v", got.Files[1])
	}
}

func TestPayloadLimitAndUnknownOperation(t *testing.T) {
	a := asset("a", "a.bin", domain.AssetFormatBinary, domain.AssetStateReady, "1234")
	_, err := Execute([]Asset{a}, Request{Operation: OperationRawRead}, application.Limits{MaxPayloadBytes: 3})
	if errorCode(t, err) != domain.CodePayloadTooLarge {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = Execute([]Asset{a}, Request{Operation: "sql"}, application.Limits{})
	if errorCode(t, err) != domain.CodeQueryInvalid {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLimitsCannotExceedHardSafetyBounds(t *testing.T) {
	got := (application.Limits{MaxPayloadBytes: application.MaxPayloadBytes + 1, MaxCSVRows: 501, MaxTextMatches: 201, MaxTextContextRunes: 201}).WithDefaults()
	if want := application.DefaultLimits(); got != want {
		t.Fatalf("limits = %#v, want %#v", got, want)
	}
}
