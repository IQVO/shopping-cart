Feature: MergeInto unifies a guest cart with the shopper's open cart
  The target is another OPEN cart that belongs to a shopper; the source is a guest cart
  or one of the same shopper. Lines merge (quantities add, clamped at 99, the merged
  list must fit 100 lines), the target's quote and selections are cleared, the source
  becomes MERGED, no event is raised and a failed merge changes neither cart.
  (use-cases.md MergeInto; aggregate canvas; troubleshooting merge-target row.)

  Background:
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500

  Scenario: Lines of the guest cart that the shopper's cart lacks are appended, the others merge
    Given an open cart
    And I add 2 of "sku-1"
    And I add 1 of "sku-2"
    And a second open cart of shopper "shopper-1"
    And I add 3 of "sku-1" to the second cart
    When I merge the first cart into the second cart
    Then the API answers 200
    And the cart has 2 lines
    And the second cart has the line "sku-1" with quantity 5
    And the second cart has the line "sku-2" with quantity 1

  Scenario: Merged quantities are clamped at 99
    Given an open cart
    And I add 60 of "sku-1"
    And a second open cart of shopper "shopper-1"
    And I add 60 of "sku-1" to the second cart
    When I merge the first cart into the second cart
    Then the API answers 200
    And the second cart has the line "sku-1" with quantity 99

  Scenario: A cart of the shopper can be merged into another cart of the same shopper
    Given an open cart of shopper "shopper-1"
    And a second open cart of shopper "shopper-1"
    When I merge the first cart into the second cart
    Then the API answers 200
    And the first cart is "MERGED"

  Scenario: A guest cart is not a merge target
    Given an open cart
    And I add 2 of "sku-1"
    And a second open cart
    When I merge the first cart into the second cart
    Then the API answers 422
    And the problem type is "merge-target"
    And the first cart is "OPEN"
    And the second cart has 0 lines

  Scenario: A cart of another shopper cannot be merged into the shopper's cart
    Given an open cart of shopper "shopper-2"
    And I add 2 of "sku-1"
    And a second open cart of shopper "shopper-1"
    When I merge the first cart into the second cart
    Then the API answers 422
    And the problem type is "merge-target"
    And the first cart is "OPEN"
    And the second cart has 0 lines

  Scenario: A checked-out target is refused and the source stays open
    Given an open cart
    And a second open cart of shopper "shopper-1"
    And checkout completes the second cart with session "sess-2"
    When I merge the first cart into the second cart
    Then the API answers 422
    And the problem type is "merge-target"
    And the first cart is "OPEN"

  Scenario: A merged target is refused too
    Given an open cart
    And a second open cart of shopper "shopper-1"
    And a third open cart of shopper "shopper-1"
    And I merge the second cart into the third cart
    When I merge the first cart into the second cart
    Then the API answers 422
    And the problem type is "merge-target"
    And the first cart is "OPEN"

  Scenario: An unknown target is not found and the source stays open
    Given an open cart
    When I merge the first cart into the unknown cart "ghost"
    Then the API answers 404
    And the problem type is "not-found"
    And the first cart is "OPEN"

  Scenario: A merge clears the target's quote and selections and raises no event
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart of shopper "shopper-1"
    And I add 1 of "sku-1"
    And I set the destination "787"
    And I quote delivery
    And I select offer "of-1" for group 1
    And a second open cart
    And I add 1 of "sku-2" to the second cart
    When I merge the second cart into the first cart
    Then the API answers 200
    And the cart has 2 lines
    And the response field "deliveryQuote" is not present
    And the response field "selections" is an empty list
    And the response field "checkoutReadiness.ready" is false
    And the events published are "CartLineAdded, CartDeliveryQuoted, CartLineAdded"

  Scenario: A merge into a cart that already holds 100 lines is refused when it needs a 101st
    Given an open cart
    And I add 1 of "sku-1"
    And a second open cart of shopper "shopper-1"
    And the second cart holds 100 lines
    When I merge the first cart into the second cart
    Then the API answers 422
    And the problem type is "too-many-lines"
    And the first cart is "OPEN"
    And the second cart has 100 lines

  Scenario: A merge into a cart that already holds 100 lines is accepted when every item is already there
    Given an open cart
    And the cart holds 1 lines
    And a second open cart of shopper "shopper-1"
    And the second cart holds 100 lines
    When I merge the first cart into the second cart
    Then the API answers 200
    And the second cart has 100 lines
    And the second cart has the line "bulk-0" with quantity 2

  Scenario: A replayed merge does not merge twice
    Given an open cart
    And I add 2 of "sku-1"
    And a second open cart of shopper "shopper-1"
    And I add 3 of "sku-1" to the second cart
    And I merge the first cart into the second cart with key "m-1"
    When I merge the first cart into the second cart with key "m-1"
    Then the API answers 200
    And the answer is a replay
    And the second cart has the line "sku-1" with quantity 5
