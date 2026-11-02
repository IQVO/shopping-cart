package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans.
const tracerName = "github.com/IQVO/shopping-cart/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card).
const (
	skillGetCart          = "get_cart"
	skillListShopperCarts = "list_shopper_carts"
)

// maxCartIDLength mirrors the REST adapter's bound on a path identifier: a
// longer cartId cannot name a cart, so it is answered 404 like REST does.
const maxCartIDLength = 128

// The list_shopper_carts skill returns the whole list, not one page: it follows
// the keyset pages of ListCarts, skillPageSize carts at a time, up to
// maxSkillCarts carts (the newest ones; documented on the Agent Card).
const (
	skillPageSize = 100
	maxSkillCarts = 1000
)

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the skill
// id and its arguments. Strict on purpose: an agent client is machine-to-
// machine, so unknown fields are rejected, never ignored, and a misspelled name
// like "stat" must not silently drop the state filter. (Go's encoding/json
// matches field names case-insensitively, so "SHOPPERID" still reaches
// shopperId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

type getCartArgs struct {
	CartID string `json:"cartId"`
}

type listShopperCartsArgs struct {
	ShopperID string `json:"shopperId"`
	// State is optional: empty means every state, like GET /carts.
	State string `json:"state,omitempty"`
}

// skillFunc runs one skill over its raw arguments.
type skillFunc func(ctx context.Context, d Deps, raw json.RawMessage) (skillResult, *problem)

// skillTable maps every advertised skill id to its implementation. A skill id
// that is not here is unknown: it gets an in-band problem and no span.
var skillTable = map[string]skillFunc{
	skillGetCart: func(ctx context.Context, d Deps, raw json.RawMessage) (skillResult, *problem) {
		return d.getCart(ctx, raw)
	},
	skillListShopperCarts: func(ctx context.Context, d Deps, raw json.RawMessage) (skillResult, *problem) {
		return d.listShopperCarts(ctx, raw)
	},
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer.
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Problem: problem}, problem.summary())
	}
	skill, known := skillTable[req.Skill]
	if !known {
		problem = problemUnknownSkill(req.Skill)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	res, problem := skill(ctx, deps, req.Arguments)
	if problem != nil {
		span.SetAttributes(attribute.Int("a2a.problem.status", problem.Status))
		if problem.Status >= 500 {
			span.SetStatus(codes.Error, problem.Title)
		}
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: &res.data}, res.summary)
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part. A
// Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered, not raised, because a
// malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformed("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformed(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformed(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformed(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// containsNUL reports whether s carries U+0000. Postgres text cannot hold it,
// so the REST adapter turns a NUL into a client error before any query; this
// surface answers the same classes (a NUL in an id that names a resource is a
// 404, in a filter a 400) instead of letting it reach a 500.
func containsNUL(s string) bool { return strings.ContainsRune(s, 0) }

// getCart is the single-cart read skill, through the same GetCart use case
// GET /carts/{cartId} uses.
func (d Deps) getCart(ctx context.Context, rawArgs json.RawMessage) (skillResult, *problem) {
	var args getCartArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillResult{}, problemMalformed(fmt.Sprintf("arguments: %v", err))
	}
	if strings.TrimSpace(args.CartID) == "" {
		return skillResult{}, problemMalformed("cartId is required")
	}
	// The same answers REST gives a path that can never name a resource.
	if containsNUL(args.CartID) {
		return skillResult{}, problemNoSuchResource()
	}
	if len(args.CartID) > maxCartIDLength {
		return skillResult{}, problemCartNotFound(args.CartID)
	}
	v, err := d.GetCart.Execute(ctx, args.CartID)
	if err != nil {
		return skillResult{}, problemFromUseCase(ctx, skillGetCart, err, args.CartID)
	}
	dto := cartOut(v)
	return skillResult{data: skillDTO{Cart: &dto}, summary: summarizeCart(dto)}, nil
}

// listShopperCarts is the shopper read skill, through the same ListCarts use
// case GET /carts uses, with the same input checks as that handler.
func (d Deps) listShopperCarts(ctx context.Context, rawArgs json.RawMessage) (skillResult, *problem) {
	var args listShopperCartsArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillResult{}, problemMalformed(fmt.Sprintf("arguments: %v", err))
	}
	if containsNUL(args.ShopperID) || containsNUL(args.State) {
		return skillResult{}, problemMalformed("a NUL character (U+0000) is not allowed in an argument")
	}
	if args.ShopperID == "" || !shared.ValidID(args.ShopperID) {
		return skillResult{}, problemValidation(shared.Invalid("shopperId is required"))
	}
	state := cart.State(args.State)
	if state != "" && !state.Valid() {
		return skillResult{}, problemValidation(shared.Invalid("state must be OPEN, CHECKED_OUT, ABANDONED or MERGED"))
	}
	list, err := d.allShopperCarts(ctx, args.ShopperID, state)
	if err != nil {
		return skillResult{}, problemFromUseCase(ctx, skillListShopperCarts, err, "")
	}
	return skillResult{data: skillDTO{Carts: &list}, summary: summarizeCarts(list)}, nil
}

// allShopperCarts follows the keyset pages of the ListCarts use case (the ones
// GET /carts serves a page at a time) until the last page, so the skill answers
// the complete list, newest first, up to maxSkillCarts carts.
func (d Deps) allShopperCarts(ctx context.Context, shopperID string, state cart.State) (cartListDTO, error) {
	list := cartListDTO{Carts: []cartDTO{}}
	var after *pagination.Keyset
	for len(list.Carts) < maxSkillCarts {
		res, err := d.ListCarts.Execute(ctx, shopperID, state, after, skillPageSize)
		if err != nil {
			return cartListDTO{}, err
		}
		for _, v := range res.Items {
			list.Carts = append(list.Carts, cartOut(v))
		}
		if res.Next == nil {
			break
		}
		after = res.Next
	}
	return list, nil
}

// problemFromUseCase maps a use case error to a problem answer with the SAME
// classification as the REST adapter's problemCatalogue for what these two
// reads can raise: a missing cart is 404 not-found and anything unrecognised is
// 500 internal-error. (The 409, 422-from-the-domain and 503 classes belong to
// writes and to the delivery-promise call this read-only agent never makes.)
// The detail is adapter-owned text: a 500 never echoes the underlying error
// (it is logged instead), so no DSN or host detail reaches an agent client.
func problemFromUseCase(ctx context.Context, skill string, err error, cartID string) *problem {
	if errors.Is(err, ports.ErrNotFound) {
		return problemCartNotFound(cartID)
	}
	slog.ErrorContext(ctx, "a2a skill failed", "skill", skill, "error", err)
	return problemInternal()
}
