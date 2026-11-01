package bdd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// jsonField reads a top-level string field of a JSON object.
func jsonField(body, name string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	s, _ := m[name].(string)
	return s
}

// lookup follows a dotted path ("lines.0.quantity") through the last answer.
func (w *world) lookup(path string) (any, bool) {
	var cur any
	if err := json.Unmarshal([]byte(w.body), &cur); err != nil {
		return nil, false
	}
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

func show(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func registerAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API answers (\d+)$`, func(code int) error {
		if w.status != code {
			return fmt.Errorf("status = %d, want %d: %s", w.status, code, w.body)
		}
		return nil
	})
	sc.Step(`^the problem type is "([^"]*)"$`, func(slug string) error {
		if got := jsonField(w.body, "type"); got != "/problems/"+slug {
			return fmt.Errorf("problem type = %q, want /problems/%s (%d %s)", got, slug, w.status, w.body)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" is the id of the second cart$`, func(path string) error { return w.fieldIs(path, w.second) })
	sc.Step(`^the response field "([^"]*)" is not present$`, func(path string) error {
		if v, ok := w.lookup(path); ok {
			return fmt.Errorf("field %s = %v, want it absent", path, v)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" is an empty list$`, func(path string) error {
		v, ok := w.lookup(path)
		if arr, isArr := v.([]any); !ok || !isArr || len(arr) != 0 {
			return fmt.Errorf("field %s = %v, want an empty list", path, v)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" is (\S+)$`, w.fieldIs)
	sc.Step(`^the cart has (\d+) lines?$`, func(n int) error { return w.countLines(n) })
	sc.Step(`^the cart still has (\d+) lines?$`, func(n int) error {
		if err := w.get(w.cartPath("")); err != nil {
			return err
		}
		return w.countLines(n)
	})
	registerWorldAssertions(sc, w)
}

func (w *world) fieldIs(path, want string) error {
	v, ok := w.lookup(path)
	if !ok || show(v) != want {
		return fmt.Errorf("field %s = %v (present %v), want %s; body %s", path, v, ok, want, w.body)
	}
	return nil
}

func (w *world) countLines(n int) error {
	v, _ := w.lookup("lines")
	if arr, _ := v.([]any); len(arr) != n {
		return fmt.Errorf("the cart has %d lines, want %d: %s", len(arr), n, w.body)
	}
	return nil
}

func registerWorldAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the events published are "([^"]*)"$`, func(list string) error {
		var got []string
		for _, ev := range w.store.Outbox().Events() {
			got = append(got, ev.EventName())
		}
		if strings.Join(got, ", ") != list {
			return fmt.Errorf("events = %v, want %s", got, list)
		}
		return nil
	})
	sc.Step(`^delivery-promise was not called$`, func() error {
		if n := len(w.quoter.Calls()); n != 0 {
			return fmt.Errorf("delivery-promise was called %d times", n)
		}
		return nil
	})
	sc.Step(`^delivery-promise was called once with destination "([^"]*)"$`, func(prefix string) error {
		calls := w.quoter.Calls()
		if len(calls) != 1 || calls[0].Destination != prefix {
			return fmt.Errorf("delivery-promise calls = %+v", calls)
		}
		return nil
	})
	registerCartAssertions(sc, w)
}

func registerCartAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the first cart is "([^"]*)"$`, func(state string) error {
		if err := w.get(w.cartPath("")); err != nil {
			return err
		}
		return w.fieldIs("state", state)
	})
	sc.Step(`^the first cart is gone$`, func() error {
		if err := w.get(w.cartPath("")); err != nil {
			return err
		}
		if w.status != http.StatusNotFound {
			return fmt.Errorf("status = %d, want 404", w.status)
		}
		return nil
	})
	sc.Step(`^the answer is a replay$`, func() error {
		if w.header.Get("Idempotency-Replayed") != "true" {
			return fmt.Errorf("the answer is not a replay: %v", w.header)
		}
		return nil
	})
	sc.Step(`^shopper "([^"]*)" has (\d+) carts?$`, func(shopper string, n int) error {
		if err := w.get("/carts?shopperId=" + shopper); err != nil {
			return err
		}
		v, _ := w.lookup("carts")
		if arr, _ := v.([]any); len(arr) != n {
			return fmt.Errorf("shopper has %d carts, want %d", len(arr), n)
		}
		return nil
	})
}
