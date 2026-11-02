Feature: Paged lists reject what the contract does not declare
  GET /carts takes shopperId, state, limit and cursor, each at most once.

  Scenario: An empty limit is refused
    When I GET "/carts?shopperId=shopper-1&limit="
    Then the API answers 400
    And the problem type is "invalid-limit"

  Scenario: A repeated limit is refused
    When I GET "/carts?shopperId=shopper-1&limit=1&limit=zzz"
    Then the API answers 400
    And the problem type is "invalid-limit"

  Scenario: A retired paging parameter is refused
    When I GET "/carts?shopperId=shopper-1&offset=0"
    Then the API answers 400
    And the problem type is "unknown-query-parameter"
