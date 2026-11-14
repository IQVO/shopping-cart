Feature: The documented bounds of a cart line
  Quantity is 1..99, a cart holds 100 lines at most, an item already in the cart
  merges instead of failing, and a refused change leaves the cart as it was.
  (use-cases.md AddLine / SetLineQuantity / RemoveLine; aggregate canvas, invariants.)

  Background:
    Given item "sku-1" is purchasable at 1000
    And an open cart

  Scenario Outline: A quantity of <quantity> is the edge of 1..99 and is accepted
    When I add <quantity> of "sku-1"
    Then the API answers 200
    And the response field "lines.0.quantity" is <quantity>

    Examples:
      | quantity |
      | 1        |
      | 99       |

  Scenario: A merge that lands exactly on 99 is accepted
    When I add 60 of "sku-1"
    And I add 39 of "sku-1"
    Then the API answers 200
    And the response field "lines.0.quantity" is 99

  Scenario: A negative quantity is out of range
    When I add -1 of "sku-1"
    Then the API answers 422
    And the problem type is "quantity-out-of-range"

  Scenario: The hundredth distinct line is accepted
    Given the cart holds 99 lines
    When I add 1 of "sku-1"
    Then the API answers 200
    And the cart has 100 lines

  Scenario: At 100 lines an item already in the cart still merges
    Given the cart holds 100 lines
    When I add 2 of "bulk-0"
    Then the API answers 200
    And the cart has 100 lines
    And the cart has the line "bulk-0" with quantity 3

  Scenario: The event of a merged add carries the merged quantity
    When I add 2 of "sku-1"
    And I add 3 of "sku-1"
    Then the CartLineAdded event for "sku-1" carries quantity 5

  Scenario: A refused add changes nothing and publishes nothing
    Given I add 2 of "sku-1"
    When I add 1 of "ghost"
    Then the problem type is "item-not-purchasable"
    And the cart has the line "sku-1" with quantity 2
    And the events published are "CartLineAdded"

  Scenario Outline: A quantity of <quantity> on an existing line is refused and the line keeps its quantity
    Given I add 2 of "sku-1"
    When I set the quantity of "sku-1" to <quantity>
    Then the API answers 422
    And the problem type is "quantity-out-of-range"
    And the cart has the line "sku-1" with quantity 2

    Examples:
      | quantity |
      | 0        |
      | 100      |

  Scenario: The quantity of a line is set, not added
    Given I add 2 of "sku-1"
    When I set the quantity of "sku-1" to 99
    Then the API answers 200
    And the response field "lines.0.quantity" is 99

  Scenario: A line that was removed cannot be removed again
    Given I add 2 of "sku-1"
    And I remove "sku-1"
    When I remove "sku-1"
    Then the API answers 404
    And the problem type is "not-found"

  Scenario: A discontinued item keeps its line, which can still be changed and removed
    Given I add 2 of "sku-1"
    And offer-availability discontinues "sku-1"
    When I read the cart
    Then the cart has 1 line
    When I set the quantity of "sku-1" to 4
    Then the response field "lines.0.quantity" is 4
    When I remove "sku-1"
    Then the cart has 0 lines
