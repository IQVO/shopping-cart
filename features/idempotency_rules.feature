Feature: Idempotency-Key and request validation behave as documented
  Every POST takes a key of 1 to 128 printable ASCII characters. The first answer below
  500 (a 4xx too) is stored for 24 hours and replayed; a 5xx is never stored. PUT and
  DELETE are idempotent by method and take none. A key belongs to one request: reusing
  it on another route or body is 409 idempotency-key-reused. A NUL is refused before any
  key is recorded. (use-cases.md; openapi Idempotency-Replayed; rest-api.md conventions.)

  Background:
    Given item "sku-1" is purchasable at 1000

  Scenario: A refused request is stored and replayed with the same refusal
    Given an open cart
    When I add 0 of "sku-1" with key "k-422"
    And I add 0 of "sku-1" with key "k-422"
    Then the API answers 422
    And the problem type is "quantity-out-of-range"
    And the answer is a replay

  Scenario: A stored refusal stays after its cause is fixed, a new key makes a new attempt
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 1 of "sku-1"
    And I quote delivery with key "q-2"
    And the problem type is "no-destination"
    And I set the destination "787"
    When I quote delivery with key "q-2"
    Then the API answers 422
    And the problem type is "no-destination"
    And the answer is a replay
    When I quote delivery with key "q-3"
    Then the API answers 200

  Scenario: A key used on another route is refused
    Given an open cart
    And I POST "/carts" with key "k-9" and body {}
    When I add 1 of "sku-1" with key "k-9"
    Then the API answers 409
    And the problem type is "idempotency-key-reused"

  Scenario: A key used for the same request on another cart is refused, not replayed
    Given an open cart
    And a second open cart
    And I quote delivery with key "q-5"
    And the problem type is "no-destination"
    When I quote delivery of the second cart with key "q-5"
    Then the API answers 409
    And the problem type is "idempotency-key-reused"

  Scenario Outline: A key of <length> characters is <verdict>
    When I create a cart with a key of <length> characters
    Then the API answers <status>

    Examples:
      | length | verdict  | status |
      | 128    | accepted | 201    |
      | 129    | refused  | 400    |

  Scenario: A key of 129 characters is not valid
    When I create a cart with a key of 129 characters
    Then the problem type is "idempotency-key-invalid"

  Scenario: A key is remembered for 24 hours and then forgotten
    Given shopper "shopper-1" creates a cart with key "t-1"
    And 23 hours pass
    When shopper "shopper-1" creates a cart with key "t-1"
    Then the API answers 201
    And the answer is a replay
    And shopper "shopper-1" has 1 cart
    When 2 hours pass
    And shopper "shopper-1" creates a cart with key "t-1"
    Then the API answers 201
    And the answer is not a replay
    And shopper "shopper-1" has 2 carts

  Scenario: PUT and DELETE are idempotent by method and take no key
    Given an open cart
    And I add 2 of "sku-1"
    When I set the destination "787" without a key
    Then the API answers 200
    And the response field "destinationPostalPrefix" is 787
    When I remove "sku-1" without a key
    Then the API answers 200
    And the cart has 0 lines

  Scenario: A NUL character is refused before its key is recorded
    When I POST "/carts" with key "k-nul" and body {"shopperId":"a\u0000b"}
    Then the API answers 400
    And the problem type is "malformed-request"
    When I POST "/carts" with key "k-nul" and body {"shopperId":"shopper-1"}
    Then the API answers 201
    And the answer is not a replay

  Scenario Outline: The body <body> is a malformed request
    Given an open cart
    When I add a line with the raw body <body>
    Then the API answers 400
    And the problem type is "malformed-request"

    Examples:
      | body                                                |
      | {"catalogItemId":"sku-1","quantity":"two"}          |
      | not json                                            |
      | {"catalogItemId":"sku-1","quantity":1,"coupon":"X"} |
      | []                                                  |

  Scenario: An identifier longer than 128 characters can never name a cart
    When I read the cart with an id of 129 characters
    Then the API answers 404
    And the problem type is "not-found"

  Scenario: A cart is created without a body, as a guest, empty and OPEN, and nothing is published
    When I create a cart with key "g-1" and no body
    Then the API answers 201
    And the response field "state" is OPEN
    And the response field "shopperId" is not present
    And the response field "version" is 1
    And the response field "estimate.totalMinor" is 0
    And the response field "estimate.estimateComplete" is false
    And the response list "lines" has 0 items
    And the events published are ""

  Scenario Outline: The shopper id "<shopper>" is refused
    When I POST "/carts" with key "k-s" and body {"shopperId":"<shopper>"}
    Then the API answers 422
    And the problem type is "validation-failed"

    Examples:
      | shopper                                                           |
      | bad id                                                            |
      | -lead                                                             |
      | aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa |

  Scenario Outline: The shopper id "<shopper>" is accepted
    When I POST "/carts" with key "k-s" and body {"shopperId":"<shopper>"}
    Then the API answers 201
    And the response field "shopperId" is <shopper>

    Examples:
      | shopper                                                          |
      | shopper:1.a_b-c                                                  |
      | aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa |
