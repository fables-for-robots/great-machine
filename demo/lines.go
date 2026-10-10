package demo

import (
	"fmt"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
)

// The colours a service writes its levels in.
const (
	dim    = "\x1b[2m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[1;31m"
	cyan   = "\x1b[36m"
	reset  = "\x1b[0m"
)

// paths are what the service web is asked for.
var paths = []string{"/", "/api/orders", "/api/orders/8841", "/api/cart", "/api/checkout", "/healthz", "/static/app.css", "/api/search?q=caf%C3%A9"}

// line makes up the next line of the service of a zathras: the stream
// and the text. Most are what a service says of its requests; among them
// are lines with tabs, with colours, with characters that are wider than
// one column, lines on standard error, and one as long as a line may be.
func (d *Demo) line(z *zathras, at time.Time) (stream, text string) {
	z.lines++
	n := z.lines
	clock := dim + at.UTC().Format("15:04:05.000") + reset
	took := fmt.Sprintf("%.1fms", 0.3+d.rng.ExpFloat64()*18)
	if z.service.name == "worker" {
		switch {
		case n%23 == 0:
			return logring.Stderr, fmt.Sprintf("%s %sWARN%s  job %d took %s, and its limit is 30s", clock, yellow, reset, 40000+n, time.Duration(31+n%20)*time.Second)
		case n%9 == 0:
			return logring.Stdout, fmt.Sprintf("%s %sINFO%s  queue jobs: %d waiting, %d in flight", clock, green, reset, d.rng.IntN(400), 1+d.rng.IntN(4))
		default:
			return logring.Stdout, fmt.Sprintf("%s %sINFO%s  job %d (resize-image) done in %s", clock, green, reset, 40000+n, took)
		}
	}
	switch {
	case n%211 == 0:
		// As long as a line may be: the ring cuts what is longer.
		head := clock + " " + cyan + "DEBUG" + reset + " request body: "
		return logring.Stdout, head + strings.Repeat("0123456789abcdef", (16<<10-len(head))/16+1)[:16<<10-len(head)]
	case n%61 == 0:
		return logring.Stderr, fmt.Sprintf("%s %sERROR%s upstream timed out after 5s: POST /api/checkout\tat checkout.submit (/build/web/checkout.go:214)\tat http.serve (/build/web/server.go:88)", clock, red, reset)
	case n%37 == 0:
		return logring.Stdout, fmt.Sprintf("%s %sINFO%s  注文 #%d を受け付けました ✓ — café Ünïcödé 🚀 — ｗｉｄｅ", clock, green, reset, 8000+n)
	case n%29 == 0:
		return logring.Stderr, fmt.Sprintf("%s %sWARN%s  pool: connection reset by peer, retrying (attempt %d)", clock, yellow, reset, 1+n%3)
	case n%17 == 0:
		return logring.Stdout, fmt.Sprintf(`{"level":"info","ts":%q,"msg":"cache refreshed","keys":%d,"took_ms":%d}`, at.UTC().Format(time.RFC3339Nano), 1000+d.rng.IntN(2000), 5+d.rng.IntN(60))
	case n%13 == 0:
		return logring.Stdout, fmt.Sprintf("GET\t%s\t200\t%s\t<b>not markup</b> &amp; \"quotes\"", paths[n%len(paths)], took)
	default:
		status, colour := 200, green
		switch {
		case n%19 == 0:
			status, colour = 404, yellow
		case n%43 == 0:
			status, colour = 502, red
		}
		method := "GET"
		if n%5 == 0 {
			method = "POST"
		}
		return logring.Stdout, fmt.Sprintf("%s %sINFO%s  http: %s %s %s%d%s %s", clock, green, reset, method, paths[n%len(paths)], colour, status, reset, took)
	}
}
