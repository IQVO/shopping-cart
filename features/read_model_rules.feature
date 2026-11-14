Feature: PurchasabilityView and EstimateView follow the events of pricing and offer-availability
  Consumers are idempotent on the CloudEvents id; price events apply only when their
  version is higher than the stored one (a withdrawal remembers its version, so a late
  older price is dropped); a discontinued item is terminal; none of these events touches
  the Cart aggregate; a payload that can never be applied is a poison message.
  (domain-events.md, consumed; use-cases.md Project PurchasabilityView and EstimateView;
  troubleshooting.md, consumers and dead letters.)

  Background:
    Given item "sku-1" is purchasable at 1000
    And item "sku-2" is purchasable at 2500
    And an open cart
    And I add 2 of "sku-1"

  Scenario Outline: A price of <price> at version <version> over a stored version 1 moves the estimate to <items>
    When pricing publishes ItemPriceChanged for "sku-1" at <price> version <version>
    And I read the cart
    Then the response field "estimate.itemsMinor" is <items>

    Examples:
      | price | version | items |
      | 1500  | 2       | 3000  |
      | 1700  | 1       | 2000  |

  Scenario: A late older price is dropped
    Given pricing publishes ItemPriceChanged for "sku-1" at 1800 version 3
    When pricing publishes ItemPriceChanged for "sku-1" at 1100 version 2
    And I read the cart
    Then the response field "estimate.itemsMinor" is 3600

  Scenario: A withdrawn price keeps the line, leaves it out of the estimate and blocks new adds
    Given I add 1 of "sku-2"
    When pricing withdraws the price of "sku-2" at version 2
    And I read the cart
    Then the cart has 2 lines
    And the response field "estimate.itemsMinor" is 2000
    When I add 1 of "sku-2"
    Then the problem type is "item-not-purchasable"

  Scenario: A price older than the withdrawal is dropped and a newer one reopens the item
    Given pricing withdraws the price of "sku-2" at version 3
    When pricing publishes ItemPriceChanged for "sku-2" at 2600 version 2
    And I add 1 of "sku-2"
    Then the problem type is "item-not-purchasable"
    When pricing publishes ItemPriceChanged for "sku-2" at 2600 version 4
    And I add 1 of "sku-2"
    Then the API answers 200

  Scenario: A withdrawal that is not newer than the price is ignored
    Given pricing withdraws the price of "sku-2" at version 1
    When I add 1 of "sku-2"
    Then the API answers 200
    Given pricing withdraws the price of "sku-2" at version 2
    When I add 1 of "sku-2"
    Then the problem type is "item-not-purchasable"

  Scenario: A price of zero is a valid price, only a negative one is poison
    Given offer-availability announces "sku-free"
    When pricing publishes ItemPriceChanged for "sku-free" at 0 version 1
    And I add 1 of "sku-free"
    Then the API answers 200

  Scenario: A discontinued item stays discontinued when the catalogue announces it again
    Given offer-availability discontinues "sku-2"
    And offer-availability announces "sku-2"
    When I add 1 of "sku-2"
    Then the problem type is "item-not-purchasable"

  Scenario: An event id already processed is ignored even when its content differs
    Given pricing publishes ItemPriceChanged for "sku-1" at 1500 version 2 as event "evt-price"
    When pricing publishes ItemPriceChanged for "sku-1" at 9000 version 3 as event "evt-price"
    And I read the cart
    Then the response field "estimate.itemsMinor" is 3000

  Scenario: Catalogue and price events never touch the cart itself
    Given I remember the cart version
    When pricing publishes ItemPriceChanged for "sku-1" at 1500 version 2
    And pricing withdraws the price of "sku-2" at version 2
    And offer-availability discontinues "sku-1"
    Then the cart version is unchanged
    And the cart has the line "sku-1" with quantity 2

  Scenario Outline: A poison event "<event>" is refused as invalid
    When the poison event "<event>" is delivered
    Then the event is refused as invalid

    Examples:
      | event                                                 |
      | CheckoutCompleted without a cartId                    |
      | CheckoutCompleted without a sessionId                 |
      | CatalogItemCreated without a catalogItemId            |
      | ItemPriceChanged with a negative price                |
      | ItemPriceChanged with a lower-case currency           |
      | ItemPriceChanged with version 0                       |
      | ItemPriceWithdrawn with version 0                     |
      | DeliveryChargeScheduleChanged without tiers           |
      | DeliveryChargeScheduleChanged with version 0          |
      | DeliveryChargeScheduleChanged with an unknown tier    |
      | TaxTableChanged without bands                         |
      | TaxTableChanged with version 0                        |
      | TaxTableChanged with an inverted band                 |

  Scenario: A refused price event leaves the item and the estimate as they were
    Given the poison event "ItemPriceChanged with a negative price" is delivered
    When I read the cart
    Then the response field "estimate.itemsMinor" is 2000
    When I add 1 of "sku-1"
    Then the API answers 200
