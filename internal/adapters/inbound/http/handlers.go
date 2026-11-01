package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// maxIDLength bounds a path identifier; a longer one cannot name a resource.
const maxIDLength = 128

// pathID returns the named path parameter, or false after writing 404 for one
// that can never name a resource.
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := chi.URLParam(r, name)
	if id == "" || len(id) > maxIDLength {
		writeError(w, r, ports.ErrNotFound)
		return "", false
	}
	return id, true
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// respond writes the cart view as status, or the error as its problem.
func respond(w http.ResponseWriter, r *http.Request, status int, v views.Cart, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, status, cartOut(v))
}

// command serves a POST or PUT that decodes body B and runs it on the cart of
// the path; the success status is 200.
func command[B any](w http.ResponseWriter, r *http.Request, run func(r *http.Request, cartID string, body B) (views.Cart, error)) {
	id, ok := pathID(w, r, "cartId")
	if !ok {
		return
	}
	var body B
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := run(r, id, body)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, err)
		return
	}
	v, err := s.CreateCart.Execute(r.Context(), req.ShopperID)
	respond(w, r, http.StatusCreated, v, err)
}

func (s *Server) handleAddLine(w http.ResponseWriter, r *http.Request) {
	command(w, r, func(r *http.Request, id string, b lineBody) (views.Cart, error) {
		return s.AddLine.Execute(r.Context(), id, b.CatalogItemID, b.Quantity)
	})
}

func (s *Server) handleSetQuantity(w http.ResponseWriter, r *http.Request) {
	item := chi.URLParam(r, "catalogItemId")
	command(w, r, func(r *http.Request, id string, b quantityRequest) (views.Cart, error) {
		return s.SetLineQuantity.Execute(r.Context(), id, item, b.Quantity)
	})
}

func (s *Server) handleRemoveLine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "cartId")
	if !ok {
		return
	}
	item, ok := pathID(w, r, "catalogItemId")
	if !ok {
		return
	}
	v, err := s.RemoveLine.Execute(r.Context(), id, item)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleSetDestination(w http.ResponseWriter, r *http.Request) {
	command(w, r, func(r *http.Request, id string, b destinationRequest) (views.Cart, error) {
		return s.SetDestination.Execute(r.Context(), id, b.DestinationPostalPrefix)
	})
}

func (s *Server) handleQuoteDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "cartId")
	if !ok {
		return
	}
	v, err := s.QuoteDelivery.Execute(r.Context(), id, r.Header.Get(IdempotencyKeyHeader))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleSelectDelivery(w http.ResponseWriter, r *http.Request) {
	command(w, r, func(r *http.Request, id string, b selectRequest) (views.Cart, error) {
		return s.SelectDelivery.Execute(r.Context(), id, b.GroupNo, b.OfferID)
	})
}

func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	command(w, r, func(r *http.Request, id string, b mergeRequest) (views.Cart, error) {
		return s.MergeInto.Execute(r.Context(), id, b.TargetCartID)
	})
}

func (s *Server) handleRequoted(w http.ResponseWriter, r *http.Request) {
	command(w, r, func(r *http.Request, id string, b requoteRequest) (views.Cart, error) {
		offers := make([]cart.QuotedOffer, len(b.Offers))
		for i, o := range b.Offers {
			offers[i] = o.domain()
		}
		return s.AcceptRequotedOffers.Execute(r.Context(), id, offers)
	})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "cartId")
	if !ok {
		return
	}
	v, err := s.GetCart.Execute(r.Context(), id)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	shopper := r.URL.Query().Get("shopperId")
	if shopper == "" || !shared.ValidID(shopper) {
		writeError(w, r, shared.Invalid("shopperId is required"))
		return
	}
	state := cart.State(r.URL.Query().Get("state"))
	if state != "" && !state.Valid() {
		writeError(w, r, shared.Invalid("state must be OPEN, CHECKED_OUT, ABANDONED or MERGED"))
		return
	}
	carts, err := s.ListCarts.Execute(r.Context(), shopper, state)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := cartListBody{Carts: make([]cartBody, len(carts))}
	for i, v := range carts {
		out.Carts[i] = cartOut(v)
	}
	writeJSON(w, http.StatusOK, out)
}
