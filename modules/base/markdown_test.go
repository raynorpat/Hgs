package base

import (
	"errors"
	"strings"
	"testing"
)

func TestProfileReadmeMarkdownBoundary(t *testing.T) {
	body := []byte("[link](guide.md) ![image](image.png)\n\n<a href=\"raw.md\">raw</a><img src=\"unsafe.png\" onerror=\"evil()\"><div data-hg-draft=\"trusted\">text</div><script>evil()</script>")
	got := string(RenderMarkdownWithURLs(body, "/code/owner/repo", func(value string, raw bool) (string, error) {
		if value == "unsafe.png" {
			return "", errors.New("blocked")
		}
		if raw {
			return "/raw/" + value, nil
		}
		return "/src/" + value, nil
	}))
	for _, want := range []string{`href="/src/guide.md"`, `src="/raw/image.png"`, `href="/src/raw.md"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s: %s", want, got)
		}
	}
	for _, bad := range []string{"unsafe.png", "onerror", "data-hg-draft", "<script"} {
		if strings.Contains(got, bad) {
			t.Errorf("unsafe %s: %s", bad, got)
		}
	}
	// URL rewriting must precede sanitization, including a malicious resolver.
	got = string(RenderMarkdownWithURLs([]byte("[link](file.md)"), "/repo", func(string, bool) (string, error) { return "javascript:alert(1)", nil }))
	if strings.Contains(got, "javascript:") {
		t.Fatal(got)
	}
}

func TestMarkdownExistingCallerURLs(t *testing.T) {
	got := string(RenderMarkdown([]byte("[guide](guide.md) ![image](image.png)"), "/code/owner/repo/src/node"))
	for _, want := range []string{`href="/code/owner/repo/src/node/guide.md"`, `src="/code/owner/repo/raw/node/image.png"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("existing caller changed: %s", got)
		}
	}
}
