Feature: A shopper lists their carts
  GET /carts?shopperId=&state= lists the shopper's carts, newest first, optionally of one
  state; shopperId is required and must be a valid id, state one of the four states
  (422 validation-failed otherwise). A guest cart has no shopper and is never listed.
  (use-cases.md GetCart and ListCarts; openapi ListCarts.)

  Scenario: A shopper lists only their own carts
    Given shopper "shopper-1" opens 2 carts
    And shopper "shopper-2" opens 1 cart
    And a second open cart
    Then shopper "shopper-1" has 2 carts
    And shopper "shopper-2" has 1 cart

  Scenario: The state filter lists only the carts in that state
    Given an open cart of shopper "shopper-1"
    And a second open cart of shopper "shopper-1"
    And checkout completes the second cart with session "sess-2"
    When I GET "/carts?shopperId=shopper-1&state=CHECKED_OUT"
    Then the response list "items" has 1 item
    And the response field "items.0.cartId" is the id of the second cart
    And the response field "items.0.state" is CHECKED_OUT
    When I GET "/carts?shopperId=shopper-1&state=OPEN"
    Then the response list "items" has 1 item
    And the response field "items.0.state" is OPEN

  Scenario Outline: The query <query> is refused as a validation failure
    When I GET "<query>"
    Then the API answers 422
    And the problem type is "validation-failed"

    Examples:
      | query                                  |
      | /carts                                 |
      | /carts?shopperId=                      |
      | /carts?shopperId=-bad                  |
      | /carts?shopperId=shopper-1&state=FROZEN |

  Scenario: The newest cart comes first
    Given an open cart of shopper "shopper-1"
    And 1 minute pass
    And a second open cart of shopper "shopper-1"
    When I GET "/carts?shopperId=shopper-1"
    Then the response field "items.0.cartId" is the id of the second cart

  Scenario: A page that ends exactly at the limit has no next cursor
    Given shopper "shopper-1" opens 3 carts
    When I GET "/carts?shopperId=shopper-1&limit=3"
    Then the response list "items" has 3 items
    And the response field "nextCursor" is not present

  Scenario: A page that is cut at the limit has a next cursor that leads to the rest
    Given shopper "shopper-1" opens 3 carts
    When I GET "/carts?shopperId=shopper-1&limit=2"
    Then the response list "items" has 2 items
    And the response field "nextCursor" exists
    When I GET the next page of "/carts?shopperId=shopper-1&limit=2"
    Then the response list "items" has 1 item
    And the response field "nextCursor" is not present
