package main

import (
	"log"

	"github.com/Santosh3204/oolio-order-food-online/pkg/config"
	"github.com/Santosh3204/oolio-order-food-online/pkg/orderfood"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	app := fiber.New()
	app.Use(recover.New())
	app.Use(logger.New()) // one line per request: status, latency, method, path

	log.Printf("config: port=%s coupon_store=%s", config.ServerPort, config.CouponConfig().Store)
	client := orderfood.NewClient()
	client.RouterService.AddRoutes(app.Group("/api"))

	addr := config.ServerAddress + ":" + config.ServerPort
	log.Printf("server started on %s", addr)
	log.Fatal(app.Listen(addr))
}
