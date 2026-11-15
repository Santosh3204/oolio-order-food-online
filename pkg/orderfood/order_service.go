package orderfood

import (
	"crypto/rand"
	"encoding/json"
	"fmt"

	"errors"

	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
	"github.com/Santosh3204/oolio-order-food-online/pkg/orderfoodtypes"
	"github.com/gofiber/fiber/v2"
)

// OrderService places orders.
type OrderService struct {
	products *ProductService
	coupons  coupon.Checker
}

// NewOrderService creates an OrderService.
func NewOrderService(p *ProductService, c coupon.Checker) *OrderService {
	return &OrderService{products: p, coupons: c}
}

func newOrderID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%X-%X-%X-%X", b[0:2], b[2:4], b[4:6], b[6:8])
}

// placeOrder handles POST /order.
func (s *OrderService) placeOrder(c *fiber.Ctx) error {
	var req orderfoodtypes.OrderReq
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "error", "invalid input")
	}
	if req.Items == nil {
		return sendError(c, fiber.StatusBadRequest, "error", "items is required")
	}
	if len(req.Items) == 0 {
		return sendError(c, fiber.StatusUnprocessableEntity, "validation", "order must contain at least one item")
	}

	if req.CouponCode != "" {
		ok, err := s.coupons.Check(req.CouponCode)
		if errors.Is(err, coupon.ErrNotReady) {
			return sendError(c, fiber.StatusServiceUnavailable, "error", "coupon service is starting, retry shortly")
		}
		if !ok {
			return sendError(c, fiber.StatusUnprocessableEntity, "validation", "invalid coupon code")
		}
	}

	products := make([]orderfoodtypes.Product, 0, len(req.Items))
	for i, it := range req.Items {
		if it.ProductID == "" {
			return sendError(c, fiber.StatusUnprocessableEntity, "validation", fmt.Sprintf("items[%d].productId is required", i))
		}
		if it.Quantity <= 0 {
			return sendError(c, fiber.StatusUnprocessableEntity, "validation", fmt.Sprintf("items[%d].quantity must be greater than 0", i))
		}
		p, ok := s.products.Get(it.ProductID)
		if !ok {
			return sendError(c, fiber.StatusUnprocessableEntity, "validation", fmt.Sprintf("items[%d]: product %q not found", i, it.ProductID))
		}
		products = append(products, p)
	}

	return c.Status(fiber.StatusOK).JSON(orderfoodtypes.Order{
		ID:       newOrderID(),
		Items:    req.Items,
		Products: products,
	})
}
