// Package query implements the bounded, deterministic in-memory query engine.
package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"secure-brain/internal/domain"
)

var decimalFloatPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

const (
	OperationRawRead    = "raw_read"
	OperationTextSearch = "text_search"
	OperationCSVQuery   = "csv_query"

	defaultMaxPayloadBytes = 25 * 1024 * 1024
	defaultMaxCSVRows      = 500
	defaultMaxTextMatches  = 200
	defaultMaxContextRunes = 200
	maxCSVOffset           = 10_000
)

// Asset combines persisted metadata with the bytes loaded for one immutable
// execution snapshot.
type Asset struct {
	Asset domain.Asset
	Bytes []byte
}

type Filter struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type Request struct {
	Operation string   `json:"operation"`
	Query     string   `json:"query,omitempty"`
	Select    []string `json:"select,omitempty"`
	Filters   []Filter `json:"filters,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Offset    int      `json:"offset,omitempty"`
}

// Limits are configurable application bounds. Non-positive values select the
// documented defaults, except request offsets and limits, which are validated.
type Limits struct {
	MaxPayloadBytes int
	MaxCSVRows      int
	MaxTextMatches  int
	MaxContextRunes int
}

func (l Limits) withDefaults() Limits {
	if l.MaxPayloadBytes <= 0 || l.MaxPayloadBytes > defaultMaxPayloadBytes {
		l.MaxPayloadBytes = defaultMaxPayloadBytes
	}
	if l.MaxCSVRows <= 0 || l.MaxCSVRows > defaultMaxCSVRows {
		l.MaxCSVRows = defaultMaxCSVRows
	}
	if l.MaxTextMatches <= 0 || l.MaxTextMatches > defaultMaxTextMatches {
		l.MaxTextMatches = defaultMaxTextMatches
	}
	if l.MaxContextRunes <= 0 || l.MaxContextRunes > defaultMaxContextRunes {
		l.MaxContextRunes = defaultMaxContextRunes
	}
	return l
}

// Execute applies one allowed query operation and creates the payload before
// Service traversal. Assets must already be in configured scope order.
func Execute(assets []Asset, req Request, limits Limits) (domain.Payload, error) {
	limits = limits.withDefaults()
	if len(assets) == 0 {
		return domain.Payload{}, queryError("The query path has no scoped assets.")
	}

	var (
		payload domain.Payload
		err     error
	)
	switch req.Operation {
	case OperationRawRead:
		payload, err = rawRead(assets)
	case OperationTextSearch:
		payload, err = textSearch(assets, req.Query, limits)
	case OperationCSVQuery:
		payload, err = csvQuery(assets, req, limits)
	default:
		return domain.Payload{}, queryError("Unknown query operation.")
	}
	if err != nil {
		return domain.Payload{}, err
	}
	if len(payload.Bytes) > limits.MaxPayloadBytes {
		return domain.Payload{}, &domain.Error{Code: domain.Code("PAYLOAD_TOO_LARGE"), Message: "The routed query payload exceeds the configured size limit."}
	}
	return payload, nil
}

type rawManifest struct {
	Operation string             `json:"operation"`
	Assets    []rawManifestAsset `json:"assets"`
}

type rawManifestAsset struct {
	AssetID    string `json:"asset_id"`
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	ByteSize   int    `json:"byte_size"`
	SHA256     string `json:"sha256"`
	DataBase64 string `json:"data_base64"`
}

func rawRead(assets []Asset) (domain.Payload, error) {
	if len(assets) == 1 {
		a := assets[0]
		return domain.Payload{
			Bytes:             a.Bytes,
			MediaType:         mediaTypeOrDefault(a.Asset.MediaType),
			SuggestedFilename: a.Asset.OriginalFilename,
			Metadata: domain.PayloadMetadata{
				"operation":   OperationRawRead,
				"asset_count": 1,
				"asset_ids":   []string{a.Asset.ID},
			},
		}, nil
	}

	manifest := rawManifest{Operation: OperationRawRead, Assets: make([]rawManifestAsset, 0, len(assets))}
	ids := make([]string, 0, len(assets))
	for _, a := range assets {
		sum := sha256.Sum256(a.Bytes)
		checksum := a.Asset.SHA256
		if checksum == "" {
			checksum = hex.EncodeToString(sum[:])
		}
		manifest.Assets = append(manifest.Assets, rawManifestAsset{
			AssetID: a.Asset.ID, Filename: a.Asset.OriginalFilename,
			MediaType: mediaTypeOrDefault(a.Asset.MediaType), ByteSize: len(a.Bytes),
			SHA256: checksum, DataBase64: base64.StdEncoding.EncodeToString(a.Bytes),
		})
		ids = append(ids, a.Asset.ID)
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		return domain.Payload{}, fmt.Errorf("marshal raw manifest: %w", err)
	}
	return domain.Payload{
		Bytes: b, MediaType: "application/json", SuggestedFilename: "raw-read.json",
		Metadata: domain.PayloadMetadata{"operation": OperationRawRead, "asset_count": len(assets), "asset_ids": ids, "structured": true},
	}, nil
}

type skippedAsset struct {
	AssetID string `json:"asset_id"`
	Reason  string `json:"reason"`
}

type textSearchResult struct {
	Operation  string         `json:"operation"`
	Query      string         `json:"query"`
	Matches    []textMatch    `json:"matches"`
	MatchCount int            `json:"match_count"`
	Truncated  bool           `json:"truncated"`
	Skipped    []skippedAsset `json:"skipped"`
}

type textMatch struct {
	AssetID    string `json:"asset_id"`
	Filename   string `json:"filename"`
	LineNumber int    `json:"line_number"`
	Context    string `json:"context"`
}

func textSearch(assets []Asset, needle string, limits Limits) (domain.Payload, error) {
	if needle == "" || !utf8.ValidString(needle) {
		return domain.Payload{}, queryError("Text search requires a non-empty UTF-8 query.")
	}
	result := textSearchResult{Operation: OperationTextSearch, Query: needle, Matches: []textMatch{}, Skipped: []skippedAsset{}}
	compatible := 0
	stop := false
	for _, a := range assets {
		if a.Asset.ProcessingState != "ready" || (a.Asset.Format != "text" && a.Asset.Format != "markdown") || !utf8.Valid(a.Bytes) {
			result.Skipped = append(result.Skipped, skippedAsset{AssetID: a.Asset.ID, Reason: "incompatible_format_or_state"})
			continue
		}
		compatible++
		if stop {
			continue
		}
		lines := strings.Split(string(a.Bytes), "\n")
		for i, line := range lines {
			line = strings.TrimSuffix(line, "\r")
			start, end, ok := findFoldRunes([]rune(line), []rune(needle))
			if !ok {
				continue
			}
			if len(result.Matches) >= limits.MaxTextMatches {
				result.Truncated = true
				stop = true
				break
			}
			result.Matches = append(result.Matches, textMatch{
				AssetID: a.Asset.ID, Filename: a.Asset.OriginalFilename,
				LineNumber: i + 1, Context: boundedContext([]rune(line), start, end, limits.MaxContextRunes),
			})
		}
	}
	if compatible == 0 {
		return domain.Payload{}, queryError("Text search is incompatible with every scoped asset.")
	}
	result.MatchCount = len(result.Matches)
	b, err := json.Marshal(result)
	if err != nil {
		return domain.Payload{}, fmt.Errorf("marshal text search result: %w", err)
	}
	return domain.Payload{
		Bytes: b, MediaType: "application/json", SuggestedFilename: "text-search.json",
		Metadata: domain.PayloadMetadata{"operation": OperationTextSearch, "match_count": result.MatchCount, "truncated": result.Truncated, "structured": true},
	}, nil
}

func boundedContext(line []rune, matchStart, matchEnd, max int) string {
	if len(line) <= max {
		return string(line)
	}
	width := matchEnd - matchStart
	if width >= max {
		return string(line[matchStart : matchStart+max])
	}
	start := matchStart - (max-width)/2
	if start < 0 {
		start = 0
	}
	if start+max > len(line) {
		start = len(line) - max
	}
	return string(line[start : start+max])
}

func findFoldRunes(haystack, needle []rune) (int, int, bool) {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return 0, 0, false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if strings.EqualFold(string(haystack[i:i+len(needle)]), string(needle)) {
			return i, i + len(needle), true
		}
	}
	return 0, 0, false
}

type csvQueryResult struct {
	Operation string          `json:"operation"`
	Files     []csvFileResult `json:"files"`
	Skipped   []skippedAsset  `json:"skipped"`
}

type csvFileResult struct {
	AssetID          string     `json:"asset_id"`
	Filename         string     `json:"filename"`
	Columns          []string   `json:"columns"`
	Rows             [][]string `json:"rows"`
	ReturnedRowCount int        `json:"returned_row_count"`
	MatchedRowCount  int        `json:"matched_row_count"`
	HasMore          bool       `json:"has_more"`
}

func csvQuery(assets []Asset, req Request, limits Limits) (domain.Payload, error) {
	if req.Offset < 0 || req.Offset > maxCSVOffset {
		return domain.Payload{}, queryError("CSV offset must be between 0 and 10000.")
	}
	if req.Limit < 0 {
		return domain.Payload{}, queryError("CSV limit cannot be negative.")
	}
	limit := req.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > limits.MaxCSVRows {
		limit = limits.MaxCSVRows
	}

	result := csvQueryResult{Operation: OperationCSVQuery, Files: []csvFileResult{}, Skipped: []skippedAsset{}}
	compatible := 0
	remaining := limit
	for _, a := range assets {
		if a.Asset.Format != "csv" || a.Asset.ProcessingState != "ready" {
			result.Skipped = append(result.Skipped, skippedAsset{AssetID: a.Asset.ID, Reason: "incompatible_format_or_state"})
			continue
		}
		compatible++
		fileResult, err := queryCSVAsset(a, req, remaining)
		if err != nil {
			return domain.Payload{}, err
		}
		result.Files = append(result.Files, fileResult)
		remaining -= fileResult.ReturnedRowCount
	}
	if compatible == 0 {
		return domain.Payload{}, queryError("CSV query is incompatible with every scoped asset.")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return domain.Payload{}, fmt.Errorf("marshal CSV query result: %w", err)
	}
	returned := 0
	for _, f := range result.Files {
		returned += f.ReturnedRowCount
	}
	return domain.Payload{
		Bytes: b, MediaType: "application/json", SuggestedFilename: "csv-query.json",
		Metadata: domain.PayloadMetadata{"operation": OperationCSVQuery, "file_count": len(result.Files), "returned_row_count": returned, "structured": true},
	}, nil
}

type columnInfo struct {
	index   int
	numeric bool
}

func queryCSVAsset(a Asset, req Request, limit int) (csvFileResult, error) {
	r := csv.NewReader(bytes.NewReader(a.Bytes))
	headers, err := r.Read()
	if err != nil {
		return csvFileResult{}, queryError("A scoped CSV asset is malformed or has no header row.")
	}
	columns := make(map[string]columnInfo, len(headers))
	for i, header := range headers {
		if header == "" {
			return csvFileResult{}, queryError("CSV headers cannot be empty.")
		}
		if _, exists := columns[header]; exists {
			return csvFileResult{}, queryError("CSV headers must be unique.")
		}
		columns[header] = columnInfo{index: i}
	}
	type inference struct {
		found   bool
		numeric bool
	}
	inferred := make([]inference, len(headers))
	for i := range inferred {
		inferred[i].numeric = true
	}
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return csvFileResult{}, queryError("A scoped CSV asset is malformed.")
		}
		for i, value := range row {
			if value == "" {
				continue
			}
			inferred[i].found = true
			if _, ok := finiteFloat(value); !ok {
				inferred[i].numeric = false
			}
		}
	}
	for name, info := range columns {
		info.numeric = inferred[info.index].found && inferred[info.index].numeric
		columns[name] = info
	}

	selected := req.Select
	if len(selected) == 0 {
		selected = append([]string(nil), headers...)
	}
	selectedIndexes := make([]int, len(selected))
	seenSelect := make(map[string]struct{}, len(selected))
	for i, name := range selected {
		info, ok := columns[name]
		if !ok {
			return csvFileResult{}, queryError("CSV select references an unknown column: " + name)
		}
		if _, duplicate := seenSelect[name]; duplicate {
			return csvFileResult{}, queryError("CSV select columns must be distinct.")
		}
		seenSelect[name] = struct{}{}
		selectedIndexes[i] = info.index
	}
	for _, filter := range req.Filters {
		if _, ok := columns[filter.Column]; !ok {
			return csvFileResult{}, queryError("CSV filter references an unknown column: " + filter.Column)
		}
		if !validOperator(filter.Operator) {
			return csvFileResult{}, queryError("CSV filter uses an unknown operator: " + filter.Operator)
		}
	}

	// Query in a second streaming pass. Type inference necessarily sees every
	// row first, but neither pass retains unselected input records in memory.
	r = csv.NewReader(bytes.NewReader(a.Bytes))
	if _, err := r.Read(); err != nil {
		return csvFileResult{}, queryError("A scoped CSV asset is malformed or has no header row.")
	}
	rows := make([][]string, 0, limit)
	matchedCount := 0
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return csvFileResult{}, queryError("A scoped CSV asset is malformed.")
		}
		ok := true
		for _, filter := range req.Filters {
			info := columns[filter.Column]
			if !filterMatches(row[info.index], filter.Operator, filter.Value, info.numeric) {
				ok = false
				break
			}
		}
		if ok {
			if matchedCount >= req.Offset && len(rows) < limit {
				projected := make([]string, len(selectedIndexes))
				for i, index := range selectedIndexes {
					projected[i] = row[index]
				}
				rows = append(rows, projected)
			}
			matchedCount++
		}
	}
	return csvFileResult{
		AssetID: a.Asset.ID, Filename: a.Asset.OriginalFilename,
		Columns: append([]string(nil), selected...), Rows: rows,
		ReturnedRowCount: len(rows), MatchedRowCount: matchedCount, HasMore: req.Offset+len(rows) < matchedCount,
	}, nil
}

func inferNumeric(rows [][]string, index int) bool {
	found := false
	for _, row := range rows {
		value := row[index]
		if value == "" {
			continue
		}
		found = true
		if _, ok := finiteFloat(value); !ok {
			return false
		}
	}
	return found
}

func validOperator(op string) bool {
	switch op {
	case "=", "!=", "contains", ">", "<", ">=", "<=":
		return true
	default:
		return false
	}
}

func filterMatches(cell, op, value string, numericColumn bool) bool {
	if op == "contains" {
		if value == "" {
			return true
		}
		_, _, ok := findFoldRunes([]rune(cell), []rune(value))
		return ok
	}
	comparison := strings.Compare(cell, value)
	if numericColumn && cell != "" {
		left, leftOK := finiteFloat(cell)
		right, rightOK := finiteFloat(value)
		if leftOK && rightOK {
			switch {
			case left < right:
				comparison = -1
			case left > right:
				comparison = 1
			default:
				comparison = 0
			}
		}
	}
	switch op {
	case "=":
		return comparison == 0
	case "!=":
		return comparison != 0
	case ">":
		return comparison > 0
	case "<":
		return comparison < 0
	case ">=":
		return comparison >= 0
	case "<=":
		return comparison <= 0
	default:
		return false
	}
}

func finiteFloat(value string) (float64, bool) {
	if !decimalFloatPattern.MatchString(value) {
		return 0, false
	}
	n, err := strconv.ParseFloat(value, 64)
	return n, err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
}

func mediaTypeOrDefault(value string) string {
	if value == "" {
		return "application/octet-stream"
	}
	return value
}

func queryError(message string) error {
	return &domain.Error{Code: domain.Code("QUERY_INVALID"), Message: message}
}
