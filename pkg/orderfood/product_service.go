package orderfood

import (
	"log"
	"strconv"

	"github.com/Santosh3204/oolio-order-food-online/pkg/orderfoodtypes"
	"github.com/gofiber/fiber/v2"
)

// ProductService serves the product catalog (in memory).
type ProductService struct {
	products []orderfoodtypes.Product
	byID     map[string]orderfoodtypes.Product
}

// NewProductService creates a service seeded with a sample catalog.
func NewProductService() *ProductService {
	list := []orderfoodtypes.Product{
		{ID: "1", Name: "Chicken Waffle", Price: 12.5, Category: "Waffle"},
		{ID: "2", Name: "Belgian Waffle", Price: 9.0, Category: "Waffle"},
		{ID: "3", Name: "Blueberry Muffin", Price: 4.25, Category: "Muffin"},
		{ID: "4", Name: "Chocolate Brownie", Price: 5.5, Category: "Brownie"},
		{ID: "5", Name: "Margherita Pizza", Price: 14.0, Category: "Pizza"},
		{ID: "6", Name: "Caesar Salad", Price: 8.75, Category: "Salad"},
	}
	m := make(map[string]orderfoodtypes.Product, len(list))
	for _, p := range list {
		m[p.ID] = p
	}
	return &ProductService{products: list, byID: m}
}

// Get returns a product by ID.
func (s *ProductService) Get(id string) (orderfoodtypes.Product, bool) {
	p, ok := s.byID[id]
	return p, ok
}

// listProducts handles GET /product.
func (s *ProductService) listProducts(c *fiber.Ctx) error {
	log.Printf("listProducts: returning %d products", len(s.products))
	return c.Status(fiber.StatusOK).JSON(s.products)
}

// getProduct handles GET /product/:productId.
func (s *ProductService) getProduct(c *fiber.Ctx) error {
	id := c.Params("productId")
	log.Printf("getProduct: id=%q", id)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		log.Printf("getProduct: invalid id %q", id)
		return sendError(c, fiber.StatusBadRequest, "error", "invalid ID supplied")
	}
	p, ok := s.Get(id)
	if !ok {
		log.Printf("getProduct: product %q not found", id)
		return sendError(c, fiber.StatusNotFound, "error", "product not found")
	}
	log.Printf("getProduct: found %q (%s)", id, p.Name)
	return c.Status(fiber.StatusOK).JSON(p)
}
