Feature: A cart holds a delivery quote and the shopper's selection
  A quote holds no capacity: it is the set of offers delivery-promise returned. A
  selection must name an offer of the current quote. Any line or destination
  change clears the quote and the selections. Checkout readiness is derived: every
  group selected and no offer expired.

  Background:
    Given item "sku-1" is purchasable at 1000
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"

  Scenario: Quoting needs a destination
    When I quote delivery
    Then the API answers 422
    And the problem type is "no-destination"
    And delivery-promise was not called

  Scenario: Quoting holds the offers and publishes CartDeliveryQuoted
    Given I set the destination "787"
    When I quote delivery
    Then the API answers 200
    And the response field "deliveryQuote.groups.0.offers.0.offerId" is of-1
    And delivery-promise was called once with destination "787"
    And the events published are "CartLineAdded, CartDeliveryQuoted"

  Scenario: Selecting an offer of the quote makes the cart ready
    Given I set the destination "787"
    And I quote delivery
    When I select offer "of-2" for group 1
    Then the response field "checkoutReadiness.ready" is true
    And the response field "selections.0.offerId" is of-2

  Scenario: A selection must name an offer of the current quote
    Given I set the destination "787"
    And I quote delivery
    When I select offer "other" for group 1
    Then the API answers 409
    And the problem type is "stale-delivery-selection"

  Scenario: A line change clears the quote and the selections
    Given I set the destination "787"
    And I quote delivery
    And I select offer "of-1" for group 1
    When I add 1 of "sku-1"
    Then the response field "deliveryQuote" is not present
    And the response field "selections" is an empty list
    And the response field "checkoutReadiness.ready" is false

  Scenario: A destination change clears the quote
    Given I set the destination "787"
    And I quote delivery
    When I set the destination "100"
    Then the response field "deliveryQuote" is not present

  Scenario: A selected offer that expired is no longer ready
    Given I set the destination "787"
    And I quote delivery
    And I select offer "of-1" for group 1
    When 31 minutes pass
    And I read the cart
    Then the response field "checkoutReadiness.noOfferExpired" is false
    And the response field "checkoutReadiness.ready" is false

  Scenario: A delivery-promise outage keeps the cart and is retryable
    Given I set the destination "787"
    And delivery-promise is unavailable
    When I quote delivery
    Then the API answers 503
    And the problem type is "delivery-promise-unavailable"
    And the cart still has 1 line

  Scenario: Accepting requoted offers writes the selection without calling delivery-promise
    Given I set the destination "787"
    And I quote delivery
    When I accept the requoted offer "re-1" NEXT_DAY for group 1 expiring in 20 minutes
    Then the API answers 200
    And the response field "selections.0.offerId" is re-1
    And the response field "checkoutReadiness.ready" is true
    And delivery-promise was called once with destination "787"

  Scenario: An empty requoted set is refused
    When I accept no requoted offers
    Then the API answers 422
    And the problem type is "validation-failed"
