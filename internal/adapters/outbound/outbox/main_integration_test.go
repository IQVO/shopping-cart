//go:build integration

package outbox_test

import (
	"os"
	"testing"

	"github.com/IQVO/shopping-cart/internal/testing/kafkatest"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	kafkatest.Shutdown()
	os.Exit(code)
}
