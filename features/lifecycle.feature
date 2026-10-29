Feature: A cart is merged, abandoned, purged and checked out
  OPEN becomes MERGED (MergeInto), ABANDONED (30 idle days) or CHECKED_OUT
  (CheckoutCompleted). A closed cart never changes. Abandoned carts are deleted by
  the policy PurgeAbandonedCarts, which publishes nothing (decision H5).

  Background:
    Given item "sku-1" is purchasable at 1000

  Scenario: A guest cart merges into the shopper's cart at sign-in
    Given an open cart
    And I add 2 of "sku-1"
    And a second open cart of shopper "shopper-1"
    And I add 1 of "sku-1" to the second cart
    When I merge the first cart into the second cart
    Then the API answers 200
    And the response field "cartId" is the id of the second cart
    And the response field "lines.0.quantity" is 3
    And the first cart is "MERGED"

  Scenario: A merged cart never changes again
    Given an open cart
    And a second open cart of shopper "shopper-1"
    And I merge the first cart into the second cart
    When I add 1 of "sku-1"
    Then the API answers 409
    And the problem type is "cart-closed"

  Scenario: A cart cannot be merged into itself
    Given an open cart of shopper "shopper-1"
    When I merge the first cart into itself
    Then the API answers 422
    And the problem type is "merge-target"

  Scenario: An idle cart is abandoned after 30 days and then purged
    Given an open cart
    When 31 days pass
    And the idle policies run
    Then the first cart is "ABANDONED"
    And the events published are "CartAbandoned"
    When 31 days pass
    And the idle policies run
    Then the first cart is gone
    And the events published are "CartAbandoned"

  Scenario: A cart changed within 30 days is not abandoned
    Given an open cart
    When 20 days pass
    And I add 1 of "sku-1"
    And 20 days pass
    And the idle policies run
    Then the first cart is "OPEN"

  Scenario: Checkout completing closes the cart and publishes CartCheckedOut
    Given an open cart
    When checkout completes the cart with session "sess-1"
    Then the first cart is "CHECKED_OUT"
    And the events published are "CartCheckedOut"

  Scenario: A repeated or unknown CheckoutCompleted is acknowledged and changes nothing
    Given an open cart
    And checkout completes the cart with session "sess-1"
    When checkout completes the cart with session "sess-1" again
    And checkout completes the unknown cart "ghost"
    Then the first cart is "CHECKED_OUT"
    And the events published are "CartCheckedOut"
