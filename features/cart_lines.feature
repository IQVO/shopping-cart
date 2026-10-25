Feature: A cart holds one line per item
  Adding an item already in the cart merges quantities (there is no duplicate
  error, decision H2). Quantity is 1..99, a cart holds 100 lines at most, and
  only a purchasable item can be added (catalogued, priced, not discontinued).
  Only AddLine publishes an event (decision H1).

  Background:
    Given item "sku-1" is purchasable at 1000
    And an open cart

  Scenario: Adding an item creates a line and publishes CartLineAdded
    When I add 2 of "sku-1"
    Then the API answers 200
    And the cart has 1 line
    And the response field "lines.0.quantity" is 2
    And the events published are "CartLineAdded"

  Scenario: Adding the same item again merges the quantities
    When I add 2 of "sku-1"
    And I add 3 of "sku-1"
    Then the cart has 1 line
    And the response field "lines.0.quantity" is 5
    And the events published are "CartLineAdded, CartLineAdded"

  Scenario: A merged quantity above 99 is refused
    When I add 60 of "sku-1"
    And I add 40 of "sku-1"
    Then the API answers 422
    And the problem type is "quantity-out-of-range"

  Scenario Outline: A quantity outside 1..99 is refused
    When I add <quantity> of "sku-1"
    Then the API answers 422
    And the problem type is "quantity-out-of-range"

    Examples:
      | quantity |
      | 0        |
      | 100      |

  Scenario: An unknown item is not purchasable
    When I add 1 of "ghost"
    Then the API answers 422
    And the problem type is "item-not-purchasable"

  Scenario: A catalogued item without a price is not purchasable
    Given item "sku-9" is catalogued but not priced
    When I add 1 of "sku-9"
    Then the problem type is "item-not-purchasable"

  Scenario: A discontinued item can no longer be added
    Given item "sku-1" is discontinued
    When I add 1 of "sku-1"
    Then the problem type is "item-not-purchasable"

  Scenario: A price that arrives before the catalogue event completes later
    Given a price of 700 arrives for "sku-7" before it is catalogued
    When I add 1 of "sku-7"
    Then the problem type is "item-not-purchasable"
    When "sku-7" is catalogued
    And I add 1 of "sku-7"
    Then the API answers 200

  Scenario: Changing a quantity and removing a line publish nothing
    Given I add 2 of "sku-1"
    When I set the quantity of "sku-1" to 5
    Then the response field "lines.0.quantity" is 5
    When I remove "sku-1"
    Then the cart has 0 lines
    And the events published are "CartLineAdded"

  Scenario: Changing a line of an unknown item is not found
    When I set the quantity of "ghost" to 2
    Then the API answers 404
    And the problem type is "not-found"

  Scenario: The hundred-and-first line is refused
    Given the cart holds 100 lines
    When I add 1 of "sku-1"
    Then the problem type is "too-many-lines"
