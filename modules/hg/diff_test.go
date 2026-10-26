package hg

import (
	"bytes"
	"errors"
	"testing"
)

func TestDiffBoundsBinaryAndLongLines(t *testing.T) {
	for _, tc := range []struct {
		name              string
		input             []byte
		truncated, binary bool
	}{
		{"empty", nil, false, false},
		{"normal", []byte("--- a/x\n+++ b/x\n+hello\n"), false, false},
		{"binary", []byte("diff -r abc x\nBinary file x has changed\n"), false, true},
		{"long line", bytes.Repeat([]byte("x"), MaxDiffBytes+1), true, false},
		{"many lines", bytes.Repeat([]byte("x\n"), MaxDiffLines+1), true, false},
		{"exact bytes", bytes.Repeat([]byte("x"), MaxDiffBytes), false, false},
		{"exact lines", bytes.Repeat([]byte("x\n"), MaxDiffLines), false, false},
		{"binary-like content", []byte("+Binary file x has changed\n"), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newDiffWriter()
			for pos := 0; pos < len(tc.input); {
				end := pos + 8191
				if end > len(tc.input) {
					end = len(tc.input)
				}
				_, err := w.Write(tc.input[pos:end])
				if errors.Is(err, ErrOutputLimit) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				pos = end
			}
			d := w.result()
			if len(d.Text) > MaxDiffBytes || bytes.Count(d.Text, []byte("\n")) > MaxDiffLines || d.Truncated != tc.truncated || d.Binary != tc.binary {
				t.Fatalf("diff bytes=%d lines=%d truncated=%v binary=%v", len(d.Text), bytes.Count(d.Text, []byte("\n")), d.Truncated, d.Binary)
			}
		})
	}
}
