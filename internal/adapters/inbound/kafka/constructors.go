package kafka

import (
	"log/slog"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// NewOfferAvailabilityConsumer reads offer-availability's topic under groupID
// (OFFER_AVAILABILITY_CONSUMER_GROUP at the composition root, never a literal).
func NewOfferAvailabilityConsumer(brokers []string, groupID string, c usecases.Consumer, logger *slog.Logger) *Consumer {
	return NewConsumerForTopic("offer-availability consumer", brokers, OfferAvailabilityTopic, groupID, OfferAvailabilityHandlers(c), logger)
}

// NewPricingConsumer reads pricing's topic under groupID
// (PRICING_CONSUMER_GROUP at the composition root).
func NewPricingConsumer(brokers []string, groupID string, c usecases.Consumer, logger *slog.Logger) *Consumer {
	return NewConsumerForTopic("pricing consumer", brokers, PricingTopic, groupID, PricingHandlers(c), logger)
}

// NewCheckoutConsumer reads checkout's topic under groupID
// (CHECKOUT_CONSUMER_GROUP at the composition root).
func NewCheckoutConsumer(brokers []string, groupID string, c usecases.Consumer, logger *slog.Logger) *Consumer {
	return NewConsumerForTopic("checkout consumer", brokers, CheckoutTopic, groupID, CheckoutHandlers(c), logger)
}

// NewConsumerForTopic builds a consumer on an explicit topic (tests isolate
// theirs). A payload that can never be applied is dead-lettered at once.
func NewConsumerForTopic(name string, brokers []string, topic, groupID string, handlers map[string]HandlerFunc, logger *slog.Logger) *Consumer {
	c := newConsumer(name, brokers, topic, groupID, handlers, logger)
	c.DeadLetterInvalid = true
	return c
}
