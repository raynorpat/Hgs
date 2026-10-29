package middleware

import (
	"net/http"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/statuspage"
)

// StatusRecovery runs inside gzip so an early panic is rendered before the
// compressor's deferred Close can commit an empty successful response.
func StatusRecovery() macaron.Handler {
	return func(c *macaron.Context) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error(4, "HTTP middleware failed: %v", v)
				if statuspage.Committed(c.Req.Request) || c.Resp.Status() >= 200 || c.Resp.Status() == 101 {
					panic(http.ErrAbortHandler)
				}
				statuspage.Render(c.Resp, c.Req.Request, statuspage.Model{Code: 500})
			}
		}()
		c.Next()
	}
}
