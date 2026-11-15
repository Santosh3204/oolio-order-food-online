package orderfood

import (
	"strings"
	"time"

	"github.com/Santosh3204/oolio-order-food-online/pkg/config"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

// RouterService registers routes.
type RouterService struct {
	client *Client
}

// NewRouterService creates a RouterService.
func NewRouterService(c *Client) *RouterService { return &RouterService{client: c} }

// clientKey identifies a caller for rate limiting. Behind a trusted proxy the
// LAST X-Forwarded-For entry is the address that proxy observed (earlier
// entries are client-supplied and spoofable).
func clientKey(trustProxy bool) func(*fiber.Ctx) string {
	return func(c *fiber.Ctx) string {
		if trustProxy {
			if xff := c.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				return strings.TrimSpace(parts[len(parts)-1])
			}
		}
		return c.IP()
	}
}

// orderLimiter limits POST /order per client; it runs before auth so floods
// and coupon-guessing are throttled whether or not the key is valid.
func orderLimiter(perMin int, trustProxy bool) fiber.Handler {
	return limiter.New(limiter.Config{
		Max:          perMin,
		Expiration:   time.Minute,
		KeyGenerator: clientKey(trustProxy),
		LimitReached: func(c *fiber.Ctx) error {
			return sendError(c, fiber.StatusTooManyRequests, "error", "rate limit exceeded, try again later")
		},
	})
}

// AddRoutes registers all API routes on the given router.
func (r *RouterService) AddRoutes(app fiber.Router) {
	app.Get("/product", r.client.ProductService.listProducts)
	app.Get("/product/:productId", r.client.ProductService.getProduct)
	app.Post("/order",
		orderLimiter(config.RateLimitPerMin, config.RateLimitTrustProxy),
		r.client.AuthService.apiKeyValidator,
		r.client.OrderService.placeOrder)
}
