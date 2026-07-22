package assets

import "testing"

func TestNormalizeObjectKey(t *testing.T) {
	good, err := NormalizeObjectKey(`\research\notes.md`)
	if err != nil || good != "research/notes.md" {
		t.Fatalf("got %q, %v", good, err)
	}
	for _, bad := range []string{"", "a//b", "../a", "a/./b", "a\x00b"} {
		if _, err := NormalizeObjectKey(bad); err == nil {
			t.Errorf("expected %q to fail", bad)
		}
	}
}

func TestClassifyAndPreview(t *testing.T) {
	csvData := []byte("name,value\na,=1\nb,2\n")
	c := Classify("x.csv", "", csvData)
	if c.Format != "csv" || c.ProcessingState != "ready" {
		t.Fatalf("unexpected classification: %+v", c)
	}
	p, err := BuildPreview(c.Format, c.ProcessingState, csvData, 100, 1)
	if err != nil || len(p.Rows) != 1 || p.Rows[0][1] != "'=1" || !p.Truncated {
		t.Fatalf("unexpected preview: %+v, %v", p, err)
	}
	if c := Classify("x.csv", "", []byte("a,b\n1\n")); c.ProcessingState != "parse_failed" {
		t.Fatalf("malformed CSV not rejected: %+v", c)
	}
}
