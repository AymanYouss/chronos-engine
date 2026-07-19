package workflows

import (
	"context"
	"fmt"
	"time"

	"github.com/AymanYouss/chronos-engine/sdk/activity"
	"github.com/AymanYouss/chronos-engine/sdk/converter"
	"github.com/AymanYouss/chronos-engine/sdk/workflow"
)

// Workflow and activity type names used for registration.
const (
	OrderWorkflowType = "OrderFulfillment"

	ActivityChargePayment    = "ChargePayment"
	ActivityReserveInventory = "ReserveInventory"
	ActivityShipOrder        = "ShipOrder"
	ActivitySendReceipt      = "SendReceipt"
)

// OrderInput is the workflow input.
type OrderInput struct {
	OrderID     string   `json:"orderId"`
	CustomerID  string   `json:"customerId"`
	AmountCents int64    `json:"amountCents"`
	Items       []string `json:"items"`
}

// OrderResult is the workflow output.
type OrderResult struct {
	OrderID       string `json:"orderId"`
	ChargeID      string `json:"chargeId"`
	ReservationID string `json:"reservationId"`
	TrackingNo    string `json:"trackingNo"`
	ReceiptID     string `json:"receiptId"`
	Status        string `json:"status"`
}

// PackagingDelay is the durable timer between reservation and shipment. It
// gives the crash-and-resume demo a clean, observable window to kill a worker
// while the workflow is durably parked.
var PackagingDelay = 5 * time.Second

// OrderFulfillment charges a payment, reserves inventory, waits out a packaging
// delay via a durable timer, ships the order, and sends a receipt. Every step
// is durable: if a worker crashes at any point, another worker replays this
// history and continues from exactly where it left off, re-running nothing.
func OrderFulfillment(ctx workflow.Context, input []byte) ([]byte, error) {
	var in OrderInput
	if err := converter.Decode(input, &in); err != nil {
		return nil, fmt.Errorf("decode order input: %w", err)
	}
	log := ctx.Logger()
	log.Info("order workflow started", "order", in.OrderID)

	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &workflow.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaxInterval:        30 * time.Second,
			MaxAttempts:        5,
		},
	}

	var chargeID string
	if err := ctx.ExecuteActivity(ActivityChargePayment, in, opts).Get(&chargeID); err != nil {
		return nil, err
	}
	log.Info("payment charged", "charge", chargeID)

	var reservationID string
	if err := ctx.ExecuteActivity(ActivityReserveInventory, in, opts).Get(&reservationID); err != nil {
		return nil, err
	}
	log.Info("inventory reserved", "reservation", reservationID)

	if err := ctx.Sleep(PackagingDelay); err != nil {
		return nil, err
	}

	var trackingNo string
	if err := ctx.ExecuteActivity(ActivityShipOrder, in, opts).Get(&trackingNo); err != nil {
		return nil, err
	}
	log.Info("order shipped", "tracking", trackingNo)

	var receiptID string
	if err := ctx.ExecuteActivity(ActivitySendReceipt, in, opts).Get(&receiptID); err != nil {
		return nil, err
	}
	log.Info("receipt sent", "receipt", receiptID)

	return converter.Encode(OrderResult{
		OrderID:       in.OrderID,
		ChargeID:      chargeID,
		ReservationID: reservationID,
		TrackingNo:    trackingNo,
		ReceiptID:     receiptID,
		Status:        "FULFILLED",
	})
}

// Activities bundles the order activities over a shared idempotency ledger.
type Activities struct {
	ledger *Ledger
}

// NewActivities builds the activity set.
func NewActivities(ledger *Ledger) *Activities { return &Activities{ledger: ledger} }

// Register wires the workflow and all activities into a worker-like registrar.
func (a *Activities) Register(r Registrar) {
	r.RegisterWorkflow(OrderWorkflowType, OrderFulfillment)
	r.RegisterActivity(ActivityChargePayment, a.chargePayment)
	r.RegisterActivity(ActivityReserveInventory, a.reserveInventory)
	r.RegisterActivity(ActivityShipOrder, a.shipOrder)
	r.RegisterActivity(ActivitySendReceipt, a.sendReceipt)
}

// Registrar is the subset of the worker used to register handlers.
type Registrar interface {
	RegisterWorkflow(name string, def workflow.Definition)
	RegisterActivity(name string, def activity.Definition)
}

func (a *Activities) chargePayment(ctx context.Context, input []byte) ([]byte, error) {
	var in OrderInput
	_ = converter.Decode(input, &in)
	info := activity.GetInfo(ctx)
	chargeID := fmt.Sprintf("ch_%s", in.OrderID)
	if err := a.record(ctx, info, "amountCents", in.AmountCents, "chargeId", chargeID); err != nil {
		return nil, err
	}
	return converter.Encode(chargeID)
}

func (a *Activities) reserveInventory(ctx context.Context, input []byte) ([]byte, error) {
	var in OrderInput
	_ = converter.Decode(input, &in)
	info := activity.GetInfo(ctx)
	reservationID := fmt.Sprintf("rsv_%s", in.OrderID)
	if err := a.record(ctx, info, "items", in.Items, "reservationId", reservationID); err != nil {
		return nil, err
	}
	return converter.Encode(reservationID)
}

func (a *Activities) shipOrder(ctx context.Context, input []byte) ([]byte, error) {
	var in OrderInput
	_ = converter.Decode(input, &in)
	info := activity.GetInfo(ctx)
	trackingNo := fmt.Sprintf("1Z%s", in.OrderID)
	if err := a.record(ctx, info, "trackingNo", trackingNo); err != nil {
		return nil, err
	}
	return converter.Encode(trackingNo)
}

func (a *Activities) sendReceipt(ctx context.Context, input []byte) ([]byte, error) {
	var in OrderInput
	_ = converter.Decode(input, &in)
	info := activity.GetInfo(ctx)
	receiptID := fmt.Sprintf("rcpt_%s", in.OrderID)
	if err := a.record(ctx, info, "customerId", in.CustomerID, "receiptId", receiptID); err != nil {
		return nil, err
	}
	return converter.Encode(receiptID)
}

// record writes the activity's side effect to the idempotency ledger. If no
// ledger is configured (unit tests), it is a no-op.
func (a *Activities) record(ctx context.Context, info activity.Info, kv ...any) error {
	if a.ledger == nil {
		return nil
	}
	payload := map[string]any{"attempt": info.Attempt}
	for i := 0; i+1 < len(kv); i += 2 {
		key, _ := kv[i].(string)
		payload[key] = kv[i+1]
	}
	return a.ledger.Record(ctx, info.WorkflowID, info.ActivityID, info.ActivityType, payload)
}
