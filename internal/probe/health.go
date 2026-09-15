package probe

import (
	"context"
	"io"
	"net/http"
	"sort"
	"time"
)

// healthTimeout bounds one probe. A service that cannot answer in two seconds
// is not healthy for a dashboard's purposes.
const healthTimeout = 2 * time.Second

// newHTTPClient builds the prober's client: bounded, and it does not follow
// redirects, so a 302 to a login page reads as down rather than up.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: healthTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// probeOne GETs url once. Any 2xx is up; everything else, including transport
// failures, is down with the reason attached.
func probeOne(ctx context.Context, c *http.Client, url string) Health {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Health{URL: url, Error: err.Error()}
	}

	start := time.Now()
	resp, err := c.Do(req)
	latency := float64(time.Since(start).Microseconds()) / 1000

	if err != nil {
		return Health{URL: url, Error: err.Error(), LatencyMS: latency}
	}
	defer resp.Body.Close()
	// Drain a little so the connection can be reused, but never a whole body.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	return Health{
		URL:       url,
		OK:        resp.StatusCode/100 == 2,
		Status:    resp.StatusCode,
		LatencyMS: latency,
	}
}

// collectHealth probes every configured URL, in name order so a slow endpoint
// delays the same neighbours every time rather than a random set.
func collectHealth(ctx context.Context, c *http.Client, urls map[string]string) map[string]Health {
	out := make(map[string]Health, len(urls))
	names := make([]string, 0, len(urls))
	for name := range urls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		out[name] = probeOne(ctx, c, urls[name])
	}
	return out
}

// Check probes one URL once, for a caller that cannot wait for the sampler's
// next pass — "is it up yet" during a start, rather than "was it up".
func Check(ctx context.Context, url string) Health {
	return probeOne(ctx, newHTTPClient(), url)
}
