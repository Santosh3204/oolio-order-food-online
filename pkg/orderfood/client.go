package orderfood

import (
	"strings"

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

// NewClient initializes all services.
func NewClient() *Client {
	var sources []coupon.Source
	for _, loc := range strings.Split(config.CouponFiles, ",") {
		if loc = strings.TrimSpace(loc); loc != "" {
			sources = append(sources, coupon.FileSource(loc))
		}
	}
	return NewClientWithCoupons(coupon.NewAsync(sources...))
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
