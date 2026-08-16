package content

import (
	"strings"
	"testing"
)

func TestCanonicalURLNormalizesOneSlash(t *testing.T) {
	if got := canonicalURL("https://moovie.example/", "/movie/1292052"); got != "https://moovie.example/movie/1292052" {
		t.Fatalf("canonicalURL() = %q", got)
	}
}

func TestJSONLDEscapesScriptTerminator(t *testing.T) {
	encoded, err := jsonLD(map[string]string{"name": "</script><script>alert(1)</script>"})
	if err != nil {
		t.Fatalf("jsonLD() error = %v", err)
	}
	if strings.Contains(string(encoded), "</script>") {
		t.Fatalf("jsonLD() returned an unescaped script terminator: %s", encoded)
	}
}
