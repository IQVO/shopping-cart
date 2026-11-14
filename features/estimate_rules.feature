Feature: The labelled estimate is computed from the documented inputs
  Effective prices, plus the selected tiers' charges (waived per group at its free-over
  threshold), plus tax at the band of the first three characters of the prefix, rounded
  half up on goods plus delivery. It is complete only when every line is priced, every
  group is selected with a known charge and a band covers the prefix. Pricing's schedule
  and tax-table events apply only when their version is higher than the stored one.
  (use-cases.md Estimate; aggregate canvas, read models; domain-events.md, consumed.)

  Scenario Outline: Prefix <prefix> falls under a tax of <tax> on goods of 2000
    Given item "sku-1" is purchasable at 1000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And an open cart
    And I add 2 of "sku-1"
    When I set the destination "<prefix>"
    Then the response field "estimate.taxMinor" is <tax>
    And the response field "estimate.totalMinor" is <total>

    Examples:
      | prefix | tax | total |
      | 700    | 200 | 2200  |
      | 799    | 200 | 2200  |
      | 699    | 0   | 2000  |
      | 800    | 0   | 2000  |
      | 7870   | 200 | 2200  |
      | 78     | 0   | 2000  |

  Scenario: The tax band is found whatever the case of the prefix
    Given item "sku-1" is purchasable at 1000
    And the tax table charges 2000 basis points for prefixes "SW1" to "SW9"
    And an open cart
    And I add 2 of "sku-1"
    When I set the destination "sw1a1aa"
    Then the response field "estimate.taxMinor" is 400

  Scenario Outline: Tax on a price of <price> is <tax>, rounded half up
    Given item "sku-r" is purchasable at <price>
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And an open cart
    And I add 1 of "sku-r"
    When I set the destination "787"
    Then the response field "estimate.taxMinor" is <tax>

    Examples:
      | price | tax |
      | 1004  | 100 |
      | 1005  | 101 |
      | 1006  | 101 |

  Scenario: A destination no band covers leaves the estimate incomplete and untaxed
    Given item "sku-1" is purchasable at 1000
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"
    And I set the destination "100"
    And I quote delivery
    When I select offer "of-1" for group 1
    Then the response field "estimate.deliveryMinor" is 1200
    And the response field "estimate.taxMinor" is 0
    And the response field "estimate.estimateComplete" is false

  Scenario: Without a tax table the estimate shows goods and delivery and is incomplete
    Given item "sku-1" is purchasable at 1000
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"
    And I set the destination "787"
    And I quote delivery
    When I select offer "of-1" for group 1
    Then the response field "estimate.totalMinor" is 3200
    And the response field "estimate.taxMinor" is 0
    And the response field "estimate.estimateComplete" is false

  Scenario Outline: Goods of <goods> in the group pay a delivery charge of <delivery> against a threshold of 5000
    Given item "sku-p" is purchasable at <price>
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-p"
    And I set the destination "787"
    And I quote delivery
    When I select offer "of-2" for group 1
    Then the response field "estimate.deliveryMinor" is <delivery>

    Examples:
      | price | goods | delivery |
      | 2499  | 4998  | 500      |
      | 2500  | 5000  | 0        |

  Scenario: Each group is charged for its own tier and judged against the threshold on its own goods
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And delivery-promise quotes group 1 for line 1 with offers "of-1" NEXT_DAY and "of-2" STANDARD expiring in 30 minutes
    And delivery-promise quotes group 2 for line 2 with offer "of-3" STANDARD expiring in 30 minutes
    And an open cart
    And I add 5 of "sku-1"
    And I add 1 of "sku-2"
    And I set the destination "787"
    And I quote delivery
    When I select offer "of-2" for group 1
    Then the response field "estimate.deliveryMinor" is 0
    And the response field "estimate.estimateComplete" is false
    When I select offer "of-3" for group 2
    Then the response field "estimate.itemsMinor" is 7500
    And the response field "estimate.deliveryMinor" is 500
    And the response field "estimate.taxMinor" is 800
    And the response field "estimate.totalMinor" is 8800
    And the response field "estimate.estimateComplete" is true
    When I select offer "of-1" for group 1
    Then the response field "estimate.deliveryMinor" is 1700
    And the response field "estimate.taxMinor" is 920
    And the response field "estimate.totalMinor" is 10120

  Scenario: The estimate currency is the one the priced items state, the configured one before that
    Given item "sku-b" is purchasable at 2500 in "BRL"
    And an open cart
    When I read the cart
    Then the response field "estimate.currency" is USD
    When I add 1 of "sku-b"
    Then the response field "estimate.currency" is BRL
    And the response field "estimate.itemsMinor" is 2500

  Scenario: A schedule of an older or equal version is ignored, a higher one replaces it
    Given item "sku-1" is purchasable at 1000
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"
    And I set the destination "787"
    And I quote delivery
    And I select offer "of-1" for group 1
    When pricing publishes charge schedule version 1 charging 9999 for NEXT_DAY
    And I read the cart
    Then the response field "estimate.deliveryMinor" is 1200
    When pricing publishes charge schedule version 2 charging 900 for NEXT_DAY
    And I read the cart
    Then the response field "estimate.deliveryMinor" is 900
    When pricing publishes charge schedule version 1 charging 5 for NEXT_DAY
    And I read the cart
    Then the response field "estimate.deliveryMinor" is 900

  Scenario: A new schedule without the selected tier leaves the delivery charge unknown
    Given item "sku-1" is purchasable at 1000
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"
    And I set the destination "787"
    And I quote delivery
    And I select offer "of-1" for group 1
    When pricing publishes charge schedule version 2 charging 500 for STANDARD
    And I read the cart
    Then the response field "estimate.deliveryMinor" is 0
    And the response field "estimate.estimateComplete" is false

  Scenario: A tax table of an older or equal version is ignored, a higher one replaces it
    Given item "sku-1" is purchasable at 1000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And an open cart
    And I add 2 of "sku-1"
    And I set the destination "787"
    When pricing publishes tax table version 1 charging 5000 basis points for prefixes "700" to "799"
    And I read the cart
    Then the response field "estimate.taxMinor" is 200
    When pricing publishes tax table version 2 charging 500 basis points for prefixes "700" to "799"
    And I read the cart
    Then the response field "estimate.taxMinor" is 100
    When pricing publishes tax table version 1 charging 5000 basis points for prefixes "700" to "799"
    And I read the cart
    Then the response field "estimate.taxMinor" is 100

  Scenario: A band of a single prefix covers that prefix
    Given item "sku-1" is purchasable at 1000
    And pricing publishes tax table version 1 charging 1000 basis points for prefixes "787" to "787"
    And an open cart
    And I add 2 of "sku-1"
    When I set the destination "787"
    Then the response field "estimate.taxMinor" is 200
    When I set the destination "788"
    Then the response field "estimate.taxMinor" is 0

  Scenario: A line whose price was withdrawn is left out of the estimate and makes it incomplete
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500
    And the charge schedule charges 1200 for NEXT_DAY and 500 for STANDARD free over 5000
    And the tax table charges 1000 basis points for prefixes "700" to "799"
    And delivery-promise offers "of-1" NEXT_DAY and "of-2" STANDARD for group 1 expiring in 30 minutes
    And an open cart
    And I add 2 of "sku-1"
    And I add 1 of "sku-2"
    And I set the destination "787"
    And I quote delivery
    And I select offer "of-1" for group 1
    And the response field "estimate.estimateComplete" is true
    When pricing withdraws the price of "sku-2" at version 2
    And I read the cart
    Then the response field "estimate.itemsMinor" is 2000
    And the response field "estimate.estimateComplete" is false
