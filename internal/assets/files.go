package assets

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"secure-brain/internal/domain"
)

type Classification struct {
	Format          domain.AssetFormat
	ProcessingState domain.AssetProcessingState
	MediaType       string
	ParseError      string
}

type Preview struct {
	Kind      string     `json:"kind"`
	Text      string     `json:"text,omitempty"`
	Headers   []string   `json:"headers,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	Truncated bool       `json:"truncated"`
}

func NormalizeObjectKey(value string) (domain.ObjectKey, error) {
	return domain.ParseObjectKey(value)
}

func Classify(filename, clientMediaType string, data []byte) Classification {
	ext := strings.ToLower(filepath.Ext(filename))
	mediaType := strings.TrimSpace(strings.Split(clientMediaType, ";")[0])
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(data)
		if guessed := mime.TypeByExtension(ext); guessed != "" {
			mediaType = guessed
		}
	}
	result := Classification{Format: domain.AssetFormatBinary, ProcessingState: domain.AssetStateReady, MediaType: mediaType}
	switch ext {
	case ".txt", ".md":
		if !utf8.Valid(data) {
			return result
		}
		if ext == ".txt" {
			result.Format = domain.AssetFormatText
		} else {
			result.Format = domain.AssetFormatMarkdown
		}
	case ".csv":
		result.Format = domain.AssetFormatCSV
		r := csv.NewReader(bytes.NewReader(data))
		r.FieldsPerRecord = 0
		if _, err := r.Read(); err != nil {
			result.ProcessingState = domain.AssetStateParseFailed
			result.ParseError = "malformed CSV"
			return result
		}
		for {
			_, err := r.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				result.ProcessingState = domain.AssetStateParseFailed
				result.ParseError = "malformed CSV"
				break
			}
		}
	}
	return result
}

func BuildPreview(format domain.AssetFormat, processingState domain.AssetProcessingState, data []byte, maxBytes, maxCSVRows int) (Preview, error) {
	if processingState == domain.AssetStateParseFailed {
		return Preview{}, errors.New("asset parsing failed")
	}
	switch format {
	case domain.AssetFormatText, domain.AssetFormatMarkdown:
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
			for !utf8.Valid(data) && len(data) > 0 {
				data = data[:len(data)-1]
			}
		}
		return Preview{Kind: string(format), Text: string(data), Truncated: truncated}, nil
	case domain.AssetFormatCSV:
		r := csv.NewReader(bytes.NewReader(data))
		headers, err := r.Read()
		if err != nil {
			return Preview{}, errors.New("asset parsing failed")
		}
		rows := make([][]string, 0, maxCSVRows)
		truncated := false
		for len(rows) <= maxCSVRows {
			row, err := r.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return Preview{}, errors.New("asset parsing failed")
			}
			if len(rows) == maxCSVRows {
				truncated = true
				break
			}
			for i, cell := range row {
				if strings.HasPrefix(cell, "=") || strings.HasPrefix(cell, "+") || strings.HasPrefix(cell, "-") || strings.HasPrefix(cell, "@") {
					row[i] = "'" + cell
				}
			}
			rows = append(rows, row)
		}
		return Preview{Kind: "csv", Headers: headers, Rows: rows, Truncated: truncated}, nil
	default:
		return Preview{Kind: "binary"}, nil
	}
}
