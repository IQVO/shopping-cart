Feature: A cart that is CHECKED_OUT, ABANDONED or MERGED never changes
  ErrCartClosed (409 cart-closed) rejects every later command; the closed cart
  can still be read. (aggregate canvas, state transitions and invariants; use-cases.md.)

  Background:
    Given item "sku-1" is purchasable at 1000
    And an open cart
    And I add 2 of "sku-1"

  Scenario Outline: A <state> cart refuses a new line and stays as it was
    Given the cart has been closed as "<state>"
    And I remember the cart version
    When I try to add a line on the closed cart
    Then the API answers 409
    And the problem type is "cart-closed"
    And the first cart is "<state>"
    And the cart version is unchanged

    Examples:
      | state       |
      | CHECKED_OUT |
      | ABANDONED   |
      | MERGED      |

  Scenario Outline: A checked-out cart refuses to <command>
    Given the cart has been closed as "CHECKED_OUT"
    And I remember the cart version
    When I try to <command> on the closed cart
    Then the API answers 409
    And the problem type is "cart-closed"
    And the cart version is unchanged

    Examples:
      | command                 |
      | set a quantity          |
      | remove a line           |
      | set the destination     |
      | quote delivery          |
      | select a delivery       |
      | accept requoted offers  |
      | merge into another cart |

  Scenario: A closed cart is not asked about delivery
    Given the cart has been closed as "CHECKED_OUT"
    When I try to quote delivery on the closed cart
    Then the problem type is "cart-closed"
    And delivery-promise was not called

  Scenario Outline: A closed cart answers cart-closed before it validates the request
    Given the cart has been closed as "CHECKED_OUT"
    When I add <quantity> of "<item>"
    Then the API answers 409
    And the problem type is "cart-closed"

    Examples:
      | quantity | item  |
      | 0        | sku-1 |
      | 5        | ghost |

  Scenario: A closed cart can still be read with its lines
    Given the cart has been closed as "CHECKED_OUT"
    When I read the cart
    Then the API answers 200
    And the response field "state" is CHECKED_OUT
    And the cart has the line "sku-1" with quantity 2
