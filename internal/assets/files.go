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
)

const MaxObjectKeyBytes = 512

type Classification struct {
	Format          string
	ProcessingState string
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

func NormalizeObjectKey(value string) (string, error) {
	value = strings.ReplaceAll(value, `\`, "/")
	value = strings.TrimPrefix(value, "/")
	if value == "" || len(value) > MaxObjectKeyBytes {
		return "", errors.New("object key must contain 1 to 512 bytes")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("object key contains an invalid segment")
		}
	}
	for _, r := range value {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", errors.New("object key contains a control character")
		}
	}
	return value, nil
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
	result := Classification{Format: "binary", ProcessingState: "ready", MediaType: mediaType}
	switch ext {
	case ".txt", ".md":
		if !utf8.Valid(data) {
			return result
		}
		if ext == ".txt" {
			result.Format = "text"
		} else {
			result.Format = "markdown"
		}
	case ".csv":
		result.Format = "csv"
		r := csv.NewReader(bytes.NewReader(data))
		r.FieldsPerRecord = 0
		if _, err := r.Read(); err != nil {
			result.ProcessingState = "parse_failed"
			result.ParseError = "malformed CSV"
			return result
		}
		for {
			_, err := r.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				result.ProcessingState = "parse_failed"
				result.ParseError = "malformed CSV"
				break
			}
		}
	}
	return result
}

func BuildPreview(format, processingState string, data []byte, maxBytes, maxCSVRows int) (Preview, error) {
	if processingState == "parse_failed" {
		return Preview{}, errors.New("asset parsing failed")
	}
	switch format {
	case "text", "markdown":
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
			for !utf8.Valid(data) && len(data) > 0 {
				data = data[:len(data)-1]
			}
		}
		return Preview{Kind: format, Text: string(data), Truncated: truncated}, nil
	case "csv":
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
