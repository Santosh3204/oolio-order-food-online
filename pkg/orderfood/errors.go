package orderfood

import (
	"log"

	"github.com/Santosh3204/oolio-order-food-online/pkg/orderfoodtypes"
	"github.com/gofiber/fiber/v2"
)

func sendError(c *fiber.Ctx, code int, typ, msg string) error {
	log.Printf("%s %s -> %d %s: %s", c.Method(), c.Path(), code, typ, msg)
	return c.Status(code).JSON(orderfoodtypes.ApiResponse{Code: code, Type: typ, Message: msg})
}
