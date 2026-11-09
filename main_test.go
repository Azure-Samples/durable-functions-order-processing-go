package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmitOrderRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{
		`not JSON`,
		`{"item":"coffee","quantity":1}`,
		`{"id":"order-1","item":" ","quantity":1}`,
		`{"id":"order-1","item":"coffee","quantity":0}`,
		`{"id":"order-1","item":"coffee","quantity":101}`,
	} {
		t.Run(body, func(t *testing.T) {
			response := httptest.NewRecorder()
			SubmitOrder(response, httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400", response.Code)
			}
		})
	}
}

func TestRecordPaymentRequiresBoolean(t *testing.T) {
	for _, body := range []string{`{}`, `{"paid":"yes"}`, `{"paid":null}`} {
		response := httptest.NewRecorder()
		RecordPayment(response, httptest.NewRequest(http.MethodPost, "/api/orders/instance-1/payment", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d; want 400", body, response.Code)
		}
	}
}

func TestSimulatedActivities(t *testing.T) {
	ctx := context.Background()
	order := Order{ID: "order-1", Item: "coffee", Quantity: 2}
	if _, err := ReserveInventory(ctx, Order{}); err == nil {
		t.Fatal("invalid order must not reserve inventory")
	}
	if _, err := ReserveInventory(ctx, order); err != nil {
		t.Fatal(err)
	}
	if _, err := ReleaseInventory(ctx, order); err != nil {
		t.Fatal(err)
	}
	tracking, err := ShipOrder(ctx, order)
	if err != nil || tracking != "demo-order-1" {
		t.Fatalf("tracking = %q, error = %v", tracking, err)
	}
}
