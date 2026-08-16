package compat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMeasureEndpointRunsBoundedConcurrentGETsAndCollectsStatuses(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: loadRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	metrics, err := MeasureEndpoint(context.Background(), client, "https://example.com", "/health", 25, 4)
	if err != nil || calls.Load() != 25 || metrics.Errors != 0 || metrics.Statuses[http.StatusOK] != 25 || metrics.P95 <= 0 {
		t.Fatalf("metrics/calls/error = %+v/%d/%v", metrics, calls.Load(), err)
	}
}

type loadRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper loadRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}
