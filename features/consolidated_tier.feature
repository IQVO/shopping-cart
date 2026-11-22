Feature: A cart accepts delivery-promise's CONSOLIDATED tier and carries its capacity band
  CONSOLIDATED is delivery-promise's opt-in tier of a customer-chosen delivery day
  (its ADR 0024). An offer of that tier may carry consolidationEligible, and any offer
  may carry a capacityBand (ADR 0023, a signal only). The cart relays both as quoted,
  lets the shopper select the offer like any other, and leaves every offer without them
  exactly as before. Unknown tiers and bands stay rejected.

  Background:
    Given item "sku-1" is purchasable at 1000
    And an open cart
    And I add 2 of "sku-1"
    And I set the destination "787"

  Scenario: A CONSOLIDATED offer can be selected and the cart is ready
    Given delivery-promise offers "of-1" STANDARD and "of-2" CONSOLIDATED eligible true in band TIGHT for group 1 expiring in 30 minutes
    And I quote delivery
    When I select offer "of-2" for group 1
    Then the API answers 200
    And the response field "selections.0.offerId" is of-2
    And the response field "checkoutReadiness.ready" is true
    And the response field "deliveryQuote.groups.0.offers.1.tier" is CONSOLIDATED
    And the response field "deliveryQuote.groups.0.offers.1.consolidationEligible" is true
    And the response field "deliveryQuote.groups.0.offers.1.capacityBand" is TIGHT

  Scenario: An offer without the optional fields is unchanged
    Given delivery-promise offers "of-1" STANDARD and "of-2" CONSOLIDATED eligible false in band SCARCE for group 1 expiring in 30 minutes
    And I quote delivery
    Then the response field "deliveryQuote.groups.0.offers.0.tier" is STANDARD
    And the response field "deliveryQuote.groups.0.offers.0.consolidationEligible" is not present
    And the response field "deliveryQuote.groups.0.offers.0.capacityBand" is not present
    And the response field "deliveryQuote.groups.0.offers.1.consolidationEligible" is false

  Scenario: A requoted CONSOLIDATED offer is accepted with its fields
    When I accept the requoted offer "re-1" CONSOLIDATED eligible true in band AMPLE for group 1 expiring in 30 minutes
    Then the API answers 200
    And the response field "selections.0.offerId" is re-1
    And the response field "deliveryQuote.groups.0.offers.0.tier" is CONSOLIDATED
    And the response field "deliveryQuote.groups.0.offers.0.consolidationEligible" is true
    And the response field "deliveryQuote.groups.0.offers.0.capacityBand" is AMPLE

  Scenario: A capacity band this service does not know is rejected
    When I accept the requoted offer "re-1" CONSOLIDATED eligible true in band FULL for group 1 expiring in 30 minutes
    Then the API answers 422

  Scenario: consolidationEligible belongs to the CONSOLIDATED tier only
    When I accept the requoted offer "re-1" STANDARD eligible true for group 1 expiring in 30 minutes
    Then the API answers 422

  Scenario: An unknown tier is still rejected
    When I accept the requoted offer "re-1" FAST for group 1 expiring in 30 minutes
    Then the API answers 422

  Scenario: A CONSOLIDATED selection has no delivery charge to estimate
    Given the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And delivery-promise offers "of-1" STANDARD and "of-2" CONSOLIDATED eligible true in band AMPLE for group 1 expiring in 30 minutes
    And I quote delivery
    When I select offer "of-2" for group 1
    Then the response field "checkoutReadiness.ready" is true
    And the response field "estimate.itemsMinor" is 2000
    And the response field "estimate.estimateComplete" is false
