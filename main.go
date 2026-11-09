package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/azure/azure-functions-golang-worker/middleware/durabletask"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/task"
)

const paymentTimeout = 2 * time.Minute

type Order struct {
	ID       string `json:"id"`
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

type Payment struct {
	Paid bool `json:"paid"`
}

type OrderResult struct {
	OrderID  string `json:"orderId"`
	Status   string `json:"status"`
	Tracking string `json:"tracking,omitempty"`
}

func (order Order) validate() error {
	if strings.TrimSpace(order.ID) == "" || len(order.ID) > 80 {
		return errors.New("id must contain between 1 and 80 bytes")
	}
	if strings.TrimSpace(order.Item) == "" || len(order.Item) > 80 {
		return errors.New("item must contain between 1 and 80 bytes")
	}
	if order.Quantity < 1 || order.Quantity > 100 {
		return errors.New("quantity must be between 1 and 100")
	}
	return nil
}

func ProcessOrder(ctx *task.OrchestrationContext) (any, error) {
	var order Order
	if err := ctx.GetInput(&order); err != nil {
		return nil, err
	}
	if err := order.validate(); err != nil {
		return nil, err
	}
	ctx.SetCustomStatus("reserving inventory")
	if err := ctx.CallActivity("ReserveInventory", task.WithActivityInput(order)).Await(nil); err != nil {
		return nil, err
	}

	ctx.SetCustomStatus("awaiting payment")
	var payment Payment
	err := ctx.WaitForSingleEvent("PaymentReceived", paymentTimeout).Await(&payment)
	if err != nil || !payment.Paid {
		ctx.SetCustomStatus("releasing inventory")
		if err := ctx.CallActivity("ReleaseInventory", task.WithActivityInput(order)).Await(nil); err != nil {
			return nil, err
		}
		ctx.SetCustomStatus("cancelled")
		return OrderResult{OrderID: order.ID, Status: "cancelled"}, nil
	}

	ctx.SetCustomStatus("shipping")
	var tracking string
	if err := ctx.CallActivity("ShipOrder", task.WithActivityInput(order)).Await(&tracking); err != nil {
		return nil, err
	}
	ctx.SetCustomStatus("shipped")
	return OrderResult{OrderID: order.ID, Status: "shipped", Tracking: tracking}, nil
}

// These activities simulate services; no real inventory or payment is changed.
func ReserveInventory(ctx context.Context, order Order) (string, error) {
	if err := order.validate(); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "inventory reservation simulated", "order_id", order.ID, "quantity", order.Quantity)
	return "reserved", nil
}

func ReleaseInventory(ctx context.Context, order Order) (string, error) {
	slog.InfoContext(ctx, "inventory release simulated", "order_id", order.ID)
	return "released", nil
}

func ShipOrder(ctx context.Context, order Order) (string, error) {
	slog.InfoContext(ctx, "shipment simulated", "order_id", order.ID)
	return fmt.Sprintf("demo-%s", order.ID), nil
}

func SubmitOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var order Order
	if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
		http.Error(w, "invalid JSON order", http.StatusBadRequest)
		return
	}
	if err := order.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client, ok := durabletask.ClientFromContext(r.Context())
	if !ok {
		http.Error(w, "durable client unavailable", http.StatusInternalServerError)
		return
	}
	id, err := client.ScheduleNewOrchestration(r.Context(), "ProcessOrder", api.WithInput(order))
	if err != nil {
		http.Error(w, "could not start order", http.StatusInternalServerError)
		return
	}
	_ = client.WriteCheckStatusResponse(w, r, string(id))
}

func GetOrderStatus(w http.ResponseWriter, r *http.Request) {
	client, ok := durabletask.ClientFromContext(r.Context())
	if !ok {
		http.Error(w, "durable client unavailable", http.StatusInternalServerError)
		return
	}
	meta, err := client.FetchOrchestrationMetadata(r.Context(), api.InstanceID(instanceID(r)))
	if errors.Is(err, api.ErrInstanceNotFound) {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not get order status", http.StatusInternalServerError)
		return
	}
	_ = durabletask.WriteStatusResponse(w, meta)
}

func RecordPayment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var payment struct {
		Paid *bool `json:"paid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payment); err != nil || payment.Paid == nil {
		http.Error(w, "provide a boolean paid field", http.StatusBadRequest)
		return
	}
	client, ok := durabletask.ClientFromContext(r.Context())
	if !ok {
		http.Error(w, "durable client unavailable", http.StatusInternalServerError)
		return
	}
	id := api.InstanceID(instanceID(r))
	meta, err := client.FetchOrchestrationMetadata(r.Context(), id)
	if errors.Is(err, api.ErrInstanceNotFound) {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not get order status", http.StatusInternalServerError)
		return
	}
	if meta.IsComplete() {
		http.Error(w, "order is already complete", http.StatusConflict)
		return
	}
	if err := client.RaiseEvent(r.Context(), id, "PaymentReceived", api.WithEventPayload(Payment{Paid: *payment.Paid})); err != nil {
		http.Error(w, "could not record payment", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func instanceID(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, part := range parts {
		if part == "orders" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func newApp() *sdk.App {
	app := sdk.FunctionApp()
	app.Use(durabletask.Middleware(
		durabletask.WithOrchestrator("ProcessOrder", ProcessOrder),
		durabletask.WithActivity("ReserveInventory", ReserveInventory),
		durabletask.WithActivity("ReleaseInventory", ReleaseInventory),
		durabletask.WithActivity("ShipOrder", ShipOrder),
	))
	app.HTTP("SubmitOrder", SubmitOrder,
		sdk.WithMethods("POST"), sdk.WithRoute("orders"), sdk.WithAuth("function"), durabletask.ClientInput())
	app.HTTP("GetOrderStatus", GetOrderStatus,
		sdk.WithMethods("GET"), sdk.WithRoute("orders/{instanceId}"), sdk.WithAuth("function"), durabletask.ClientInput())
	app.HTTP("RecordPayment", RecordPayment,
		sdk.WithMethods("POST"), sdk.WithRoute("orders/{instanceId}/payment"), sdk.WithAuth("function"), durabletask.ClientInput())
	return app
}

func main() {
	worker.Start(newApp())
}
