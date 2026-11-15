package orderfood

import "github.com/gofiber/fiber/v2"

// RouterService registers routes.
type RouterService struct {
	client *Client
}

// NewRouterService creates a RouterService.
func NewRouterService(c *Client) *RouterService { return &RouterService{client: c} }

// AddRoutes registers all API routes on the given router.
func (r *RouterService) AddRoutes(app fiber.Router) {
	app.Get("/product", r.client.ProductService.listProducts)
	app.Get("/product/:productId", r.client.ProductService.getProduct)
	app.Post("/order", r.client.AuthService.apiKeyValidator, r.client.OrderService.placeOrder)
}
