Feature: Every POST takes an Idempotency-Key
  The first answer below 500 is stored for 24 hours: the same key and request
  replays it, the same key with another request is refused.

  Scenario: A POST without a key is refused
    When I POST "/carts" without a key and body {}
    Then the API answers 400
    And the problem type is "idempotency-key-required"

  Scenario: A replay returns the stored answer and creates nothing twice
    When I POST "/carts" with key "k1" and body {"shopperId":"shopper-1"}
    And I POST "/carts" with key "k1" and body {"shopperId":"shopper-1"}
    Then the API answers 201
    And the answer is a replay
    And shopper "shopper-1" has 1 cart

  Scenario: The same key with another request is refused
    When I POST "/carts" with key "k1" and body {"shopperId":"shopper-1"}
    And I POST "/carts" with key "k1" and body {"shopperId":"shopper-2"}
    Then the API answers 409
    And the problem type is "idempotency-key-reused"

  Scenario: A malformed body is a problem document
    When I POST "/carts" with key "k2" and body {"unknown":1}
    Then the API answers 400
    And the problem type is "malformed-request"

  Scenario: An unknown cart is not found
    When I POST "/carts/ghost/lines" with key "k3" and body {"catalogItemId":"sku-1","quantity":1}
    Then the API answers 404
    And the problem type is "not-found"
