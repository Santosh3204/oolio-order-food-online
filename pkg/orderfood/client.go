package orderfood

import (
	"github.com/Santosh3204/oolio-order-food-online/pkg/config"
	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
)

// Client wires together the services of the API.
type Client struct {
	ProductService *ProductService
	OrderService   *OrderService
	AuthService    *AuthService
	RouterService  *RouterService
}

// NewClient initializes all services; the coupon index is loaded in the background.
func NewClient() *Client {
	return NewClientWithCoupons(coupon.NewService(config.CouponConfig()))
}

// NewClientWithCoupons initializes all services with the given coupon checker.
func NewClientWithCoupons(checker coupon.Checker) *Client {
	c := &Client{}
	c.ProductService = NewProductService()
	c.OrderService = NewOrderService(c.ProductService, checker)
	c.AuthService = NewAuthService(config.APIKey)
	c.RouterService = NewRouterService(c)
	return c
}
