import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/shopping-cart-api",
    },
    {
      type: "category",
      label: "Carts",
      link: {
        type: "doc",
        id: "api-reference/rest/carts",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/create-cart",
          label: "Create a cart",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-carts",
          label: "List carts of a shopper",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-cart",
          label: "Get a cart with its quote, selections and estimate",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/merge-cart-into",
          label: "Merge this cart into another OPEN cart of the same shopper",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Lines",
      link: {
        type: "doc",
        id: "api-reference/rest/lines",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/add-cart-line",
          label: "Add a line to the cart (merges into an existing line of the item)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/set-cart-line-quantity",
          label: "Set the quantity of a cart line",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/remove-cart-line",
          label: "Remove a cart line",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Delivery",
      link: {
        type: "doc",
        id: "api-reference/rest/delivery",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/set-cart-destination",
          label: "Set the destination postal prefix",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/quote-cart-delivery",
          label: "Quote delivery offers for the cart",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/select-cart-delivery",
          label: "Select a delivery offer for a shipment group",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/accept-requoted-offers",
          label: "Write the offers of a checkout requote into the cart",
          className: "api-method post",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
