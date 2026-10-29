Feature: The cart shows a labelled, non-binding estimate
  Effective prices plus the selected tiers' charges plus the destination's tax
  band. The binding total is the checkout session's PriceQuote. Without a
  destination only the item subtotal shows and estimateComplete is false
  (decision H6).

  Background:
    Given item "sku-1" is purchasable at 1000
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"

  Scenario: Without a destination the estimate is the item subtotal only
    When I read the cart
    Then the response field "estimate.itemsMinor" is 2000
    And the response field "estimate.totalMinor" is 2000
    And the response field "estimate.estimateComplete" is false

  Scenario: With a destination, a quote and a selection the estimate is complete
    Given I set the destination "787"
    And I quote delivery
    When I select offer "of-1" for group 1
    Then the response field "estimate.deliveryMinor" is 1200
    And the response field "estimate.taxMinor" is 320
    And the response field "estimate.totalMinor" is 3520
    And the response field "estimate.estimateComplete" is true

  Scenario: Delivery is free when the group goods reach the free-over threshold
    Given I add 3 of "sku-1"
    And I set the destination "787"
    And I quote delivery
    When I select offer "of-2" for group 1
    Then the response field "estimate.deliveryMinor" is 0
    And the response field "estimate.totalMinor" is 5500

  Scenario: An unselected group leaves the estimate incomplete
    Given I set the destination "787"
    When I quote delivery
    Then the response field "estimate.estimateComplete" is false
    And the response field "estimate.deliveryMinor" is 0

  Scenario: A new price changes the estimate
    Given a price of 1500 arrives for "sku-1" with version 2
    When I read the cart
    Then the response field "estimate.itemsMinor" is 3000
