package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type rateLimitError struct{ until time.Time }

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("HTTP 429: retry after %s", e.until.Format(time.RFC3339))
}

func RetryAfter(err error) time.Time {
	var limited *rateLimitError
	if errors.As(err, &limited) {
		return limited.until
	}
	return time.Time{}
}

type rateLimitTransport struct{}

func (rateLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusTooManyRequests {
		return response, err
	}
	defer response.Body.Close()
	until := retryDeadline(response.Header.Get("Retry-After"), time.Now())
	return nil, &rateLimitError{until: until}
}

func retryDeadline(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date
	}
	return now.Add(5 * time.Minute)
}

func rateLimitedClient() *http.Client { return &http.Client{Transport: rateLimitTransport{}} }
