package orderfood

import (
	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

type fake struct{}

func (fake) IsValid(c string) bool { return c == "HAPPYHRS" }

func newApp() *fiber.App {
	app := fiber.New()
	NewClientWithCoupons(coupon.Static{Validator: fake{}}).RouterService.AddRoutes(app.Group("/api"))
	return app
}

func do(t *testing.T, method, path, body, key string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("api_key", key)
	}
	resp, err := newApp().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestAPI(t *testing.T) {
	good := `{"items":[{"productId":"1","quantity":2}]}`
	cases := []struct {
		name, method, path, body, key string
		want                          int
	}{
		{"list", "GET", "/api/product", "", "", 200},
		{"get", "GET", "/api/product/1", "", "", 200},
		{"get bad id", "GET", "/api/product/abc", "", "", 400},
		{"get missing", "GET", "/api/product/999", "", "", 404},
		{"order ok", "POST", "/api/order", good, "apitest", 200},
		{"order no key", "POST", "/api/order", good, "", 401},
		{"order bad key", "POST", "/api/order", good, "nope", 403},
		{"order bad json", "POST", "/api/order", "{", "apitest", 400},
		{"order no items", "POST", "/api/order", `{}`, "apitest", 400},
		{"order good coupon", "POST", "/api/order", `{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":1}]}`, "apitest", 200},
		{"order bad coupon", "POST", "/api/order", `{"couponCode":"SUPER100","items":[{"productId":"1","quantity":1}]}`, "apitest", 422},
		{"order empty items", "POST", "/api/order", `{"items":[]}`, "apitest", 422},
		{"order unknown product", "POST", "/api/order", `{"items":[{"productId":"99","quantity":1}]}`, "apitest", 422},
		{"order zero qty", "POST", "/api/order", `{"items":[{"productId":"1","quantity":0}]}`, "apitest", 422},
	}
	for _, c := range cases {
		if got := do(t, c.method, c.path, c.body, c.key); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
