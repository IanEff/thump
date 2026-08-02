package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ianeff/thump/internal/httpx"
)

func TestInstantQuery_DecodesAValueFromA200Response(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Errorf("want query=up in the request, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{},"value":[1688745600,"1"]}
		]}}`))
	}))
	defer ts.Close()

	result, err := httpx.InstantQuery(t.Context(), nil, ts.URL, "up")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Result) != 1 {
		t.Fatalf("want one result, got %d", len(result.Data.Result))
	}
}

func TestInstantQuery_ANon200ResponseReturnsAStatusError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := httpx.InstantQuery(t.Context(), nil, ts.URL, "up")
	statusErr, ok := errors.AsType[*httpx.StatusError](err)
	if !ok {
		t.Fatalf("want a *StatusError, got %v", err)
	}
	if statusErr.Status != "500 Internal Server Error" {
		t.Errorf("want the response's Status text carried verbatim, got %q", statusErr.Status)
	}
}

func TestInstantQuery_AnUndecodableBodyWrapsErrDecodeInstantResult(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, err := httpx.InstantQuery(t.Context(), nil, ts.URL, "up")
	if !errors.Is(err, httpx.ErrDecodeInstantResult) {
		t.Fatalf("want ErrDecodeInstantResult, got %v", err)
	}
}

func TestInstantQuery_AnUnparseableBaseURLWrapsErrBuildInstantQuery(t *testing.T) {
	t.Parallel()
	_, err := httpx.InstantQuery(t.Context(), nil, "://not a url", "up")
	if !errors.Is(err, httpx.ErrBuildInstantQuery) {
		t.Fatalf("want ErrBuildInstantQuery, got %v", err)
	}
}
