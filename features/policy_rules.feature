Feature: The cart lifecycle policies are driven by the clock and by checkout
  AbandonInactive: an OPEN cart with no change for 30 days becomes ABANDONED and
  CartAbandoned carries lastChangedAt. PurgeAbandonedCarts: a cart that is not OPEN
  and idle for 30 days is deleted, silently. CloseOnCheckoutCompleted: an OPEN cart
  becomes CHECKED_OUT; for any other cart the event is acknowledged and changes
  nothing. (use-cases.md; aggregate canvas, Corrective Policies; business-context.md.)
  The cart was created at 2026-10-25T10:00:00Z.

  Background:
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500
    And an open cart
    And I add 2 of "sku-1"
    And I add 1 of "sku-2"

  Scenario: A cart idle for exactly 30 days is abandoned and the event names the last change
    Given 30 days, 0 hours and 0 minutes pass
    When the AbandonInactive policy runs
    Then the first cart is "ABANDONED"
    And the events published are "CartLineAdded, CartLineAdded, CartAbandoned"
    And the CartAbandoned event carries lastChangedAt "2026-10-25T10:00:00Z"

  Scenario: A cart idle for one minute less than 30 days is not abandoned
    Given 29 days, 23 hours and 59 minutes pass
    When the AbandonInactive policy runs
    Then the first cart is "OPEN"
    And the events published are "CartLineAdded, CartLineAdded"

  Scenario Outline: A change (<change>) on day 20 restarts the 30 days
    Given 20 days pass
    And I <change>
    And 20 days pass
    When the AbandonInactive policy runs
    Then the first cart is "OPEN"

    Examples:
      | change                           |
      | set the quantity of "sku-2" to 3 |
      | remove "sku-2"                   |
      | set the destination "787"        |

  Scenario: Reading a cart is not a change and does not restart the 30 days
    Given 20 days pass
    And I read the cart
    And 11 days pass
    When the AbandonInactive policy runs
    Then the first cart is "ABANDONED"

  Scenario: Only the idle carts are abandoned
    Given a second open cart of shopper "shopper-1"
    And 20 days pass
    And I add 1 of "sku-1" to the second cart
    And 11 days pass
    When the AbandonInactive policy runs
    Then the first cart is "ABANDONED"
    And the second cart is "OPEN"

  Scenario: A second sweep abandons nothing more
    Given 31 days pass
    And the AbandonInactive policy runs
    When the AbandonInactive policy runs
    Then the events published are "CartLineAdded, CartLineAdded, CartAbandoned"

  Scenario Outline: A <state> cart is never abandoned, however long it idles
    Given the cart has been closed as "<state>"
    And 31 days pass
    When the AbandonInactive policy runs
    Then the first cart is "<state>"

    Examples:
      | state       |
      | CHECKED_OUT |
      | MERGED      |

  Scenario Outline: A <state> cart is purged once it has been idle for 30 days
    Given the cart has been closed as "<state>"
    And 31 days pass
    When the PurgeAbandonedCarts policy runs
    Then the first cart is gone

    Examples:
      | state       |
      | CHECKED_OUT |
      | ABANDONED   |
      | MERGED      |

  Scenario: A closed cart is kept until the 30th idle day and deleted on it
    Given the cart has been closed as "CHECKED_OUT"
    And 29 days, 23 hours and 59 minutes pass
    When the PurgeAbandonedCarts policy runs
    Then the first cart is "CHECKED_OUT"
    When 1 minute passes
    And the PurgeAbandonedCarts policy runs
    Then the first cart is gone

  Scenario: An open cart is never purged
    Given 31 days pass
    When the PurgeAbandonedCarts policy runs
    Then the first cart is "OPEN"

  Scenario: A purged cart disappears from its shopper's list
    Given an open cart of shopper "shopper-1"
    And the cart has been closed as "CHECKED_OUT"
    And shopper "shopper-1" has 1 cart
    And 31 days pass
    When the PurgeAbandonedCarts policy runs
    Then shopper "shopper-1" has 0 carts

  Scenario: Checkout completing a cart records the session that closed it
    When checkout completes the cart with session "sess-77"
    Then the first cart is "CHECKED_OUT"
    And the CartCheckedOut event carries session "sess-77"

  Scenario: CheckoutCompleted closes the cart it names and no other
    Given a second open cart of shopper "shopper-1"
    When checkout completes the second cart with session "sess-2"
    Then the second cart is "CHECKED_OUT"
    And the first cart is "OPEN"

  Scenario Outline: CheckoutCompleted for a cart in state <state> is acknowledged and changes nothing
    Given the cart has been closed as "<state>"
    When checkout completes the cart with session "sess-9"
    Then the first cart is "<state>"
    And the events published are "<events>"

    Examples:
      | state     | events                                      |
      | ABANDONED | CartLineAdded, CartLineAdded, CartAbandoned |
      | MERGED    | CartLineAdded, CartLineAdded                |

  Scenario: An event id the cart has already seen is never applied again
    Given CheckoutCompleted event "evt-1" names the unknown cart "ghost"
    When CheckoutCompleted event "evt-1" names the first cart with session "sess-1"
    Then the first cart is "OPEN"
    And the events published are "CartLineAdded, CartLineAdded"
