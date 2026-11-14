Feature: Liveness and readiness are separate
  /healthz is liveness and never flips; /readyz is the drain gate plus the Postgres and
  Kafka checks: 503 not_ready once shutdown has started, 503 dependency-unavailable
  while a dependency is unreachable. (troubleshooting.md, Boot and readiness.)

  Scenario: A running service is live and ready
    When I GET "/healthz"
    Then the API answers 200
    And the response field "status" is ok
    When I GET "/readyz"
    Then the API answers 200
    And the response field "status" is ready

  Scenario: A draining service stops being ready but stays live
    Given the service starts draining
    When I GET "/readyz"
    Then the API answers 503
    And the response field "status" is not_ready
    When I GET "/healthz"
    Then the API answers 200

  Scenario: An unreachable broker makes the service not ready and names it
    Given kafka is unreachable
    When I GET "/readyz"
    Then the API answers 503
    And the problem type is "dependency-unavailable"
    And the response field "detail" reads "not ready: kafka"
