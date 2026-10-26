package repo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/middleware"
)

type historyCursor struct {
	Version  int    `json:"v"`
	Node     string `json:"node"`
	Revision int64  `json:"rev"`
	Before   int64  `json:"before"`
	Path     string `json:"path"`
}

func encodeHistoryCursor(q hg.HistoryQuery) string {
	b, _ := json.Marshal(historyCursor{1, string(q.Snapshot), q.SnapshotRevision, q.BeforeRevision, base64.RawURLEncoding.EncodeToString([]byte(q.Path))})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeHistoryCursor(token string) (hg.HistoryQuery, error) {
	bad := hg.ErrInvalidCursor
	q := hg.HistoryQuery{}
	if len(token) == 0 || len(token) > 8192 {
		return q, bad
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return q, bad
	}
	var c historyCursor
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return q, bad
	}
	if d.Decode(new(interface{})) != io.EOF {
		return q, bad
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(b, canonical) {
		return q, bad
	}
	node, err := hg.ParseNodeID(c.Node)
	if err != nil || c.Version != 1 || c.Revision < 0 || c.Before < -1 || c.Before > c.Revision {
		return q, bad
	}
	p, err := base64.RawURLEncoding.Strict().DecodeString(c.Path)
	if err != nil || len(p) > 4096 {
		return q, bad
	}
	// Apply the same path grammar as source/raw links, including raw byte tokens.
	if len(p) > 0 {
		u, _ := url.Parse("/commits?path=" + base64.RawURLEncoding.EncodeToString(p))
		if _, err = middleware.ParseHgPath(u, "/commits"); err != nil {
			return q, bad
		}
	}
	q = hg.HistoryQuery{Snapshot: node, SnapshotRevision: c.Revision, BeforeRevision: c.Before, Path: string(p)}
	return q, nil
}
func historyURL(repo string, q hg.HistoryQuery) string {
	return repo + "/commits?cursor=" + encodeHistoryCursor(q)
}
func displayPath(s string) string { return strings.ToValidUTF8(s, "�") }
