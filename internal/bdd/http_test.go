package bdd

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// send performs one request against the router and records the answer.
func (w *world) send(method, path, key, body string) error {
	req, err := http.NewRequest(method, w.api().URL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	w.status, w.body, w.header = resp.StatusCode, string(b), resp.Header
	return nil
}

// post sends a POST with a fresh Idempotency-Key.
func (w *world) post(path, body string) error {
	w.keys++
	return w.send(http.MethodPost, path, fmt.Sprintf("key-%d", w.keys), body)
}

// put and del send a fresh Idempotency-Key as well: PUT and DELETE are
// idempotent by method, so the service accepts the key and ignores it.
func (w *world) put(path, body string) error {
	w.keys++
	return w.send(http.MethodPut, path, fmt.Sprintf("key-%d", w.keys), body)
}

func (w *world) del(path string) error {
	w.keys++
	return w.send(http.MethodDelete, path, fmt.Sprintf("key-%d", w.keys), "")
}

func (w *world) get(path string) error { return w.send(http.MethodGet, path, "", "") }

// mustPost is post for setup steps: any non-2xx answer fails the step.
func (w *world) mustPost(path, body string) error {
	if err := w.post(path, body); err != nil {
		return err
	}
	if w.status/100 != 2 {
		return fmt.Errorf("POST %s answered %d: %s", path, w.status, w.body)
	}
	return nil
}
