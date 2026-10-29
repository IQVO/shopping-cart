// Package ids is the production ports.IDGenerator: a cart id is "cart-" and
// 12 random hexadecimal characters.
package ids

import (
	"strings"

	"github.com/google/uuid"

	"github.com/IQVO/shopping-cart/internal/application/ports"
)

// Generator mints ids.
type Generator struct{}

var _ ports.IDGenerator = Generator{}

// NewCartID returns "cat-" followed by 12 random hexadecimal characters.
func (Generator) NewCartID() string {
	return "cart-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}
