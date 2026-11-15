package main

import (
	"log"

	"github.com/Santosh3204/oolio-order-food-online/pkg/config"
	"github.com/Santosh3204/oolio-order-food-online/pkg/orderfood"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	app := fiber.New()
	app.Use(recover.New())

	client := orderfood.NewClient()
	client.RouterService.AddRoutes(app.Group("/api"))

	addr := config.ServerAddress + ":" + config.ServerPort
	log.Printf("server started on %s", addr)
	log.Fatal(app.Listen(addr))
}
