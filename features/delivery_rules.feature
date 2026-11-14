Feature: The delivery quote and the selection follow the documented rules
  A quote holds no capacity: it is what delivery-promise returned. A selection names
  an offer of the CURRENT quote, one pick per shipment group. Any line or
  destination change clears quote and selections; nothing else does.
  Readiness is derived: every group selected and no selected offer expired.
  (use-cases.md QuoteDelivery / SelectDelivery; business-context.md decisions 2 and 3.)

  Background:
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500
    And an open cart
    And I add 2 of "sku-1"
    And I add 1 of "sku-2"
    And I set the destination "787"

  Scenario: delivery-promise is asked about exactly the cart's lines and prefix
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    When I quote delivery
    Then the API answers 200
    And delivery-promise was asked for lines "sku-1 x2, sku-2 x1" at "787"

  Scenario: A two-group quote keeps each group's lines and offers and reports its group count
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And delivery-promise quotes group 2 for line 2 with offer "of-3" STANDARD expiring in 30 minutes
    When I quote delivery
    Then the response list "deliveryQuote.groups" has 2 items
    And the response field "deliveryQuote.groups.1.lineNos.0" is 2
    And the response field "deliveryQuote.groups.1.offers.0.offerId" is of-3
    And the CartDeliveryQuoted event carries prefix "787" and 2 groups

  Scenario: The tiers delivery-promise could not offer are kept with their constraint
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And delivery-promise reports SAME_DAY unavailable with the constraint "cut-off-passed"
    When I quote delivery
    Then the response field "deliveryQuote.unavailable.0.tier" is SAME_DAY
    And the response field "deliveryQuote.unavailable.0.constraint" is cut-off-passed
    And the response field "deliveryQuote.unavailableTiers.0" is SAME_DAY

  Scenario: Nothing can be selected before a quote exists
    When I select offer "of-1" for group 1
    Then the API answers 409
    And the problem type is "stale-delivery-selection"

  Scenario Outline: Offer <offer> cannot be selected for group <group>, which it does not belong to
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And delivery-promise quotes group 2 for line 2 with offer "of-3" STANDARD expiring in 30 minutes
    And I quote delivery
    When I select offer "<offer>" for group <group>
    Then the API answers 409
    And the problem type is "stale-delivery-selection"

    Examples:
      | offer | group |
      | of-3  | 1     |
      | of-1  | 2     |
      | of-1  | 3     |

  Scenario: A two-group cart is ready only when both groups are selected
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And delivery-promise quotes group 2 for line 2 with offer "of-3" STANDARD expiring in 30 minutes
    And I quote delivery
    When I select offer "of-1" for group 1
    Then the response field "checkoutReadiness.allGroupsSelected" is false
    And the response field "checkoutReadiness.ready" is false
    When I select offer "of-3" for group 2
    Then the response field "checkoutReadiness.allGroupsSelected" is true
    And the response field "checkoutReadiness.ready" is true

  Scenario: Selecting again for a group replaces the earlier pick and keeps the quote
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    When I select offer "of-2" for group 1
    Then the response list "selections" has 1 item
    And the response field "selections.0.offerId" is of-2
    And the response field "deliveryQuote.groups.0.offers.0.offerId" is of-1

  Scenario: Quoting again replaces the quote and drops the old selection
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    And delivery-promise offers "of-9" NEXT_DAY and "of-10" STANDARD for group 1 expiring in 30 minutes
    When I quote delivery
    Then the response field "deliveryQuote.groups.0.offers.0.offerId" is of-9
    And the response field "selections" is an empty list
    And the response field "checkoutReadiness.ready" is false
    When I select offer "of-1" for group 1
    Then the problem type is "stale-delivery-selection"

  Scenario Outline: <change> clears the quote and the selection
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    When I <change>
    Then the API answers 200
    And the response field "deliveryQuote" is not present
    And the response field "selections" is an empty list
    And the response field "checkoutReadiness.ready" is false

    Examples:
      | change                                |
      | set the quantity of "sku-2" to 3      |
      | remove "sku-2"                        |
      | set the destination "100"             |

  Scenario: A refused change leaves the quote and the selection in place
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    When I add 1 of "ghost"
    Then the problem type is "item-not-purchasable"
    When I set the quantity of "sku-2" to 100
    Then the problem type is "quantity-out-of-range"
    When I read the cart
    Then the response field "deliveryQuote.groups.0.offers.0.offerId" is of-1
    And the response field "selections.0.offerId" is of-1
    And the response field "checkoutReadiness.ready" is true

  Scenario: A price move never clears the quote, it moves the estimate
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    When pricing publishes ItemPriceChanged for "sku-1" at 1500 version 2
    And I read the cart
    Then the response field "deliveryQuote.groups.0.offers.0.offerId" is of-1
    And the response field "selections.0.offerId" is of-1
    And the response field "checkoutReadiness.ready" is true
    And the response field "estimate.itemsMinor" is 5500

  Scenario: A selected offer is valid until it expires; the selection stays and only readiness changes
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    When 29 minutes pass
    And I read the cart
    Then the response field "checkoutReadiness.ready" is true
    When 2 minutes pass
    And I read the cart
    Then the response field "checkoutReadiness.ready" is false
    And the response field "checkoutReadiness.allGroupsSelected" is true
    And the response field "checkoutReadiness.noOfferExpired" is false
    And the response field "selections.0.offerId" is of-1
    And the response field "state" is OPEN

  Scenario: After the offers expire a new quote and selection make the cart ready again
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    And 31 minutes pass
    And delivery-promise offers "of-5" NEXT_DAY and "of-6" STANDARD for group 1 expiring in 30 minutes
    When I quote delivery
    And I select offer "of-6" for group 1
    Then the response field "checkoutReadiness.ready" is true
    And the response field "selections.0.offerId" is of-6

  Scenario: A delivery-promise outage publishes no quote event
    Given delivery-promise is unavailable
    When I quote delivery
    Then the problem type is "delivery-promise-unavailable"
    And the events published are "CartLineAdded, CartLineAdded"

  Scenario: A request delivery-promise rejects is reported as unavailable
    Given delivery-promise rejects the request
    When I quote delivery
    Then the API answers 503
    And the problem type is "delivery-promise-unavailable"
    And the cart still has 2 lines

  Scenario: A quote retried with the same key after an outage runs again once delivery-promise is back
    Given delivery-promise is unavailable
    And I quote delivery with key "q-1"
    And the problem type is "delivery-promise-unavailable"
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    When I quote delivery with key "q-1"
    Then the API answers 200
    And the answer is not a replay
    And delivery-promise received the idempotency key "q-1" 2 times
    And the events published are "CartLineAdded, CartLineAdded, CartDeliveryQuoted"

  Scenario Outline: The destination "<prefix>" is <verdict>
    When I set the destination "<prefix>"
    Then the API answers <status>

    Examples:
      | prefix      | verdict                              | status |
      | 7870000000  | accepted, 10 letters or digits       | 200    |
      | 78700000000 | refused, longer than 10 characters   | 422    |
      | 78-700      | refused, not letters or digits       | 422    |
      | 787 00      | refused, an address is never stored  | 422    |
      | 78@00       | refused, between digits and letters  | 422    |
      | 78_00       | refused, between capitals and lower  | 422    |
      | 78{00       | refused, past the lower-case letters | 422    |
      | 78z00AZ09   | accepted, letters and digits         | 200    |
      |             | refused, empty                       | 422    |

  Scenario: A refused destination keeps the one the cart had
    When I set the destination "78-700"
    Then the problem type is "validation-failed"
    When I read the cart
    Then the response field "destinationPostalPrefix" is 787
