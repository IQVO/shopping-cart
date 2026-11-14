Feature: AcceptRequotedOffers replaces the quote and selects, without asking delivery-promise
  Checkout hands back the offers of its own requote, one per group. They REPLACE the
  held quote, each is the selection of its group, delivery-promise is never called
  and no event is raised. (aggregate canvas, Handled Commands; eventstorming board 1.)

  Background:
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And an open cart
    And I add 2 of "sku-1"
    And I add 1 of "sku-2"
    And I set the destination "787"

  Scenario: Requoted offers replace the held quote and its other offers
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    When I accept these requoted offers "re-1/1/NEXT_DAY"
    Then the response list "deliveryQuote.groups.0.offers" has 1 item
    And the response field "deliveryQuote.groups.0.offers.0.offerId" is re-1
    When I select offer "of-1" for group 1
    Then the problem type is "stale-delivery-selection"

  Scenario: A requote of fewer groups drops the selection of a group the new quote no longer has
    Given delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And delivery-promise quotes group 2 for line 2 with offer "of-3" STANDARD expiring in 30 minutes
    And I quote delivery
    And I select offer "of-1" for group 1
    And I select offer "of-3" for group 2
    When I accept these requoted offers "re-1/1/NEXT_DAY"
    Then the response list "selections" has 1 item
    And the response list "deliveryQuote.groups" has 1 item
    And the response field "selections.0.offerId" is re-1

  Scenario: A group keeps the line numbers of the previous quote's group of the same number
    Given delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And I quote delivery
    When I accept these requoted offers "re-1/1/NEXT_DAY"
    Then the response list "deliveryQuote.groups.0.lineNos" has 1 item
    And the response field "deliveryQuote.groups.0.lineNos.0" is 1

  Scenario: Without a previous quote a single requoted group spans every line
    When I accept these requoted offers "re-1/1/NEXT_DAY"
    Then the response list "deliveryQuote.groups.0.lineNos" has 2 items
    And the response field "checkoutReadiness.ready" is true

  Scenario: The requoted tier drives the estimate and nothing is published
    When I accept these requoted offers "re-1/1/NEXT_DAY"
    Then the response field "estimate.itemsMinor" is 4500
    And the response field "estimate.deliveryMinor" is 1200
    And the response field "estimate.taxMinor" is 570
    And the response field "estimate.totalMinor" is 6270
    And the response field "estimate.estimateComplete" is true
    And delivery-promise was not called
    And the events published are "CartLineAdded, CartLineAdded"

  Scenario Outline: A requote that breaks the rule "<rule>" is refused
    When I accept these requoted offers <offers>
    Then the API answers 422
    And the problem type is "validation-failed"
    And the response field "deliveryQuote" is not present

    Examples:
      | rule                    | offers                                      |
      | one offer per group     | "re-1/1/NEXT_DAY" "re-2/1/STANDARD"         |
      | distinct offer ids      | "re-1/1/NEXT_DAY" "re-1/2/STANDARD"         |
      | a known tier            | "re-1/1/OVERNIGHT"                          |
      | a group number from 1   | "re-1/0/NEXT_DAY"                           |
