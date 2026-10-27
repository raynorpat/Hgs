package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Unknwon/macaron"
	"github.com/macaron-contrib/session"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
)

type memSession struct {
	data map[interface{}]interface{}
}

func (m *memSession) Set(k, v interface{}) error { m.data[k] = v; return nil }
func (m *memSession) Get(k interface{}) interface{} {
	if m.data == nil {
		return nil
	}
	return m.data[k]
}
func (m *memSession) Delete(k interface{}) error { delete(m.data, k); return nil }
func (m *memSession) ID() string                 { return "test" }
func (m *memSession) Release() error             { return nil }
func (m *memSession) Flush() error               { return nil }
func (m *memSession) Read(string) (session.RawStore, error) {
	return m, nil
}
func (m *memSession) Destory(*macaron.Context) error { return nil }
func (m *memSession) RegenerateId(*macaron.Context) (session.RawStore, error) {
	return m, nil
}
func (m *memSession) Count() int { return len(m.data) }
func (m *memSession) GC()        {}

func TestSignedInUserTrustsOnlyConfiguredProxies(t *testing.T) {
	dir := t.TempDir()
	oldDb, oldHas, oldLog := models.DbCfg, models.HasEngine, setting.LogRootPath
	oldService, oldCIDRs, oldHeader := setting.Service, setting.TrustedProxyCIDRs, setting.ReverseProxyAuthUser
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(dir, "gogs.db"))
	setting.LogRootPath = dir
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg, models.HasEngine, setting.LogRootPath = oldDb, oldHas, oldLog
		setting.Service, setting.TrustedProxyCIDRs, setting.ReverseProxyAuthUser = oldService, oldCIDRs, oldHeader
	})
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	models.HasEngine = true
	setting.ReverseProxyAuthUser = "X-WEBAUTH-USER"
	setting.Service.EnableReverseProxyAuth = true
	setting.Service.EnableReverseProxyAutoRegister = false

	erin := &models.User{Name: "erin", Email: "erin@example.com", Passwd: "erin-password", IsActive: true}
	alice := &models.User{Name: "alice", Email: "alice@example.com", Passwd: "alice-password", IsActive: true}
	bob := &models.User{Name: "bob", Email: "bob@example.com", Passwd: "bob-password", IsActive: true}
	for _, u := range []*models.User{erin, alice, bob} {
		if err := models.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	bob.IsActive = false
	if err := models.UpdateUser(bob); err != nil {
		t.Fatal(err)
	}

	req := func(remote, header, user, pass string, uid int64) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if header != "" {
			r.Header.Set("X-WEBAUTH-USER", header)
		}
		if user != "" {
			r.SetBasicAuth(user, pass)
		}
		return r
	}
	sess := func(uid int64) session.Store {
		s := &memSession{data: map[interface{}]interface{}{}}
		if uid != 0 {
			s.data["uid"] = uid
		}
		return s
	}

	setting.TrustedProxyCIDRs = nil
	if u, basic := SignedInUser(req("192.0.2.10:9", "alice", "", "", 0), sess(0)); u != nil || basic {
		t.Fatalf("empty proxy list accepted %+v", u)
	}
	setting.TrustedProxyCIDRs = []string{"not-a-cidr", "192.0.2.0/24"}
	if u, _ := SignedInUser(req("198.51.100.10:9", "alice", "", "", 0), sess(0)); u != nil {
		t.Fatalf("untrusted proxy accepted %+v", u)
	}
	if u, basic := SignedInUser(req("192.0.2.10:9", "alice", "", "", 0), sess(0)); basic || u == nil || u.Id != alice.Id {
		t.Fatalf("trusted proxy: basic %v user %+v", basic, u)
	}
	if u, _ := SignedInUser(req("192.0.2.10:9", "bob", "", "", 0), sess(0)); u != nil {
		t.Fatalf("inactive proxy user accepted %+v", u)
	}
	if u, basic := SignedInUser(req("198.51.100.10:9", "mallory", "alice", "alice-password", 0), sess(0)); !basic || u == nil || u.Id != alice.Id {
		t.Fatalf("basic auth: basic %v user %+v", basic, u)
	}
	if u, basic := SignedInUser(req("198.51.100.10:9", "", "bob", "bob-password", 0), sess(0)); u != nil || basic {
		t.Fatalf("inactive basic auth accepted %+v", u)
	}
	if u, basic := SignedInUser(req("198.51.100.10:9", "bob", "", "", 0), sess(alice.Id)); basic || u == nil || u.Id != alice.Id {
		t.Fatalf("session: basic %v user %+v", basic, u)
	}
	if u, _ := SignedInUser(req("198.51.100.10:9", "", "", "", 0), sess(bob.Id)); u != nil {
		t.Fatalf("inactive session accepted %+v", u)
	}
}
