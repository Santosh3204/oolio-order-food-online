package orderfood

import (
	"crypto/subtle"
	"log"

	"github.com/gofiber/fiber/v2"
)

// AuthService validates the api_key header.
type AuthService struct {
	apiKey string
}

// NewAuthService creates an AuthService expecting the given key.
func NewAuthService(apiKey string) *AuthService { return &AuthService{apiKey: apiKey} }

// apiKeyValidator returns 401 when the key is missing and 403 when it is wrong.
func (a *AuthService) apiKeyValidator(c *fiber.Ctx) error {
	key := c.Get("api_key")
	if key == "" {
		log.Printf("auth: missing api_key on %s %s", c.Method(), c.Path())
		return sendError(c, fiber.StatusUnauthorized, "error", "unauthorized")
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(a.apiKey)) != 1 {
		log.Printf("auth: invalid api_key on %s %s", c.Method(), c.Path())
		return sendError(c, fiber.StatusForbidden, "error", "forbidden")
	}
	log.Printf("auth: api_key accepted for %s %s", c.Method(), c.Path())
	return c.Next()
}
