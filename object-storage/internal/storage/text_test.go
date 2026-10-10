package storage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedTextPreviewUTF8AndInvalidEncoding(t *testing.T) {
	for _, extra := range []string{"中", "😀"} {
		text := strings.Repeat("a", 1023) + extra + strings.Repeat("b", 1024)
		for _, advertised := range []int64{int64(len(text)), 1} {
			reader := strings.NewReader(text)
			raw, truncated, err := previewText(reader, advertised, 1024)
			if err != nil || !truncated || len(raw) != 1023 || !utf8.Valid(raw) || reader.Len() != len(text)-1025 {
				t.Fatal("bounded UTF-8 preview", err, len(raw), reader.Len())
			}
		}
	}
	for _, text := range []string{"a\x00b", "a\xffb"} {
		if _, _, err := previewText(strings.NewReader(text), int64(len(text)), 1024); err == nil {
			t.Fatal("invalid text accepted")
		}
	}
	for _, n := range []int{0, 1023, 1024, 1025} {
		raw, truncated, err := previewText(strings.NewReader(strings.Repeat("x", n)), int64(n), 1024)
		if err != nil || len(raw) > 1024 || truncated != (n > 1024) {
			t.Fatal("limit boundary", n, err)
		}
	}
	for _, ext := range []string{"md", "txt", "log", "sh", "bat", "conf", "cfg", "py", "java", "xml", "sql", "hql", "json", "yml", "yaml", "ini", "js"} {
		if !textKey("file." + strings.ToUpper(ext)) {
			t.Fatal("missing extension", ext)
		}
	}
	if textKey("file.exe") {
		t.Fatal("binary extension")
	}
}
