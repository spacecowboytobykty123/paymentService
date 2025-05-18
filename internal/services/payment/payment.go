package payment

import (
	"context"
	"fmt"
	"github.com/spacecowboytobykty123/paymentProto/gen/go/payment"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/customer"
	"github.com/stripe/stripe-go/v82/paymentintent"
	"github.com/stripe/stripe-go/v82/paymentmethod"
	subscription2 "github.com/stripe/stripe-go/v82/subscription"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	contextkeys "paymentService/internal/contextkey"
	"paymentService/internal/data"
	"paymentService/internal/jsonlog"
	"strconv"
	"time"
)

// Payment represents the payment service that handles Stripe integration
type Payment struct {
	log      *jsonlog.Logger
	tokenTTL time.Duration
}

//type paymentProvider interface {
//	CreateSubscription(ctx context.Context, planID int32, paymentMethod string) (string, payment.Status)
//	CancelSubscription(ctx context.Context, stripeSubID string) payment.Status
//	GetSubscription(ctx context.Context, stripeSubID string) data.Subscription
//}

func New(log *jsonlog.Logger, tokenTTL time.Duration, stripeKey string) *Payment {
	stripe.Key = stripeKey
	return &Payment{
		log:      log,
		tokenTTL: tokenTTL,
	}
}

type StripeClient struct{}

func NewStripeClient(secretKey string) *StripeClient {
	stripe.Key = secretKey
	return &StripeClient{}
}

// CreateSubscription creates a new subscription in Stripe
// This method handles the complete flow of creating a subscription:
// 1. Get or create a customer for the user
// 2. Attach the payment method to the customer
// 3. Set the payment method as default for the customer
// 4. Create the subscription with the specified plan
// 5. Handle any payment confirmation if needed
func (p Payment) CreateSubscription(ctx context.Context, planID int32, paymentMethodID string) (string, payment.Status) {
	// Step 1: Get user ID from context
	userID, err := getUserFromContext(ctx)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "CreateSubscription",
			"planID":    strconv.Itoa(int(planID)),
		})
		return "", payment.Status_STATUS_INVALID_USER
	}

	// Convert user ID to string for Stripe operations
	userIDStr := strconv.FormatInt(userID, 10)

	// Step 2: Validate the plan ID by mapping it to a Stripe price ID
	stripePrice := mapPlanIDToStripePrice(planID)
	if stripePrice == "" {
		err := fmt.Errorf("invalid plan ID: %d", planID)
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "CreateSubscription",
			"planID":    strconv.Itoa(int(planID)),
			"userID":    userIDStr,
		})
		return "", payment.Status_STATUS_INVALID_PLAN
	}

	// Step 3: Validate payment method ID
	if paymentMethodID == "" {
		err := fmt.Errorf("payment method ID is required")
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "CreateSubscription",
			"planID":    strconv.Itoa(int(planID)),
			"userID":    userIDStr,
		})
		return "", payment.Status_STATUS_INVALID_PAYMENT_METHOD
	}

	p.log.PrintInfoWithContext(ctx, "Starting subscription creation process", map[string]string{
		"operation":      "CreateSubscription",
		"planID":         strconv.Itoa(int(planID)),
		"userID":         userIDStr,
		"paymentMethod":  paymentMethodID,
	})

	// Step 4: Get or create a customer for the user
	customerID, err := p.getOrCreateCustomer(ctx, userIDStr)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":     "CreateSubscription",
			"planID":        strconv.Itoa(int(planID)),
			"userID":        userIDStr,
			"paymentMethod": paymentMethodID,
			"error":         "Failed to get or create customer: " + err.Error(),
		})
		return "", payment.Status_STATUS_INTERNAL_ERROR
	}

	// Step 5: Attach the payment method to the customer
	err = p.attachPaymentMethod(ctx, customerID, paymentMethodID)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":     "CreateSubscription",
			"planID":        strconv.Itoa(int(planID)),
			"userID":        userIDStr,
			"customerID":    customerID,
			"paymentMethod": paymentMethodID,
			"error":         "Failed to attach payment method: " + err.Error(),
		})
		return "", payment.Status_STATUS_INVALID_PAYMENT_METHOD
	}

	// Step 6: Set the payment method as default for the customer
	err = p.setDefaultPaymentMethod(ctx, customerID, paymentMethodID)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":     "CreateSubscription",
			"planID":        strconv.Itoa(int(planID)),
			"userID":        userIDStr,
			"customerID":    customerID,
			"paymentMethod": paymentMethodID,
			"error":         "Failed to set default payment method: " + err.Error(),
		})
		// Continue anyway, as this is not critical
	}

	// Step 7: Create the subscription
	// Configure subscription parameters
	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{
				Price: stripe.String(stripePrice),
			},
		},
		PaymentBehavior: stripe.String("default_incomplete"), // Handle payment confirmation if needed
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
		},
	}

	// Expand the latest invoice and payment intent for detailed information
	params.AddExpand("latest_invoice.payment_intent")

	// Create the subscription in Stripe
	subscription, err := subscription2.New(params)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":     "CreateSubscription",
			"planID":        strconv.Itoa(int(planID)),
			"userID":        userIDStr,
			"customerID":    customerID,
			"paymentMethod": paymentMethodID,
			"stripeError":   err.Error(),
		})
		return "", payment.Status_STATUS_INTERNAL_ERROR
	}

	// Step 8: Log the subscription details
	p.log.PrintInfoWithContext(ctx, "Subscription created successfully", map[string]string{
		"operation":          "CreateSubscription",
		"planID":             strconv.Itoa(int(planID)),
		"userID":             userIDStr,
		"customerID":         customerID,
		"subscriptionID":     subscription.ID,
		"subscriptionStatus": string(subscription.Status),
	})

	// Step 9: Return the subscription ID and success status
	return subscription.ID, payment.Status_STATUS_OK
}

// CancelSubscription cancels an existing subscription in Stripe
// This method handles the complete flow of canceling a subscription:
// 1. Validate the subscription ID
// 2. Cancel the subscription in Stripe
// 3. Verify the cancellation was successful
func (p Payment) CancelSubscription(ctx context.Context, stripeSubID string) payment.Status {
	// Step 1: Validate subscription ID
	if stripeSubID == "" {
		err := fmt.Errorf("subscription ID is required")
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "CancelSubscription",
		})
		return payment.Status_STATUS_INTERNAL_ERROR
	}

	p.log.PrintInfoWithContext(ctx, "Starting subscription cancellation process", map[string]string{
		"operation":      "CancelSubscription",
		"subscriptionID": stripeSubID,
	})

	// Step 2: Retrieve the subscription to verify it exists and check its current status
	subParams := &stripe.SubscriptionParams{}
	existingSub, err := subscription2.Get(stripeSubID, subParams)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":      "CancelSubscription",
			"subscriptionID": stripeSubID,
			"error":          "Failed to retrieve subscription: " + err.Error(),
		})
		return payment.Status_STATUS_INTERNAL_ERROR
	}

	// If subscription is already canceled, return success
	if existingSub.Status == stripe.SubscriptionStatusCanceled {
		p.log.PrintInfoWithContext(ctx, "Subscription is already cancelled", map[string]string{
			"operation":      "CancelSubscription",
			"subscriptionID": stripeSubID,
			"status":         string(existingSub.Status),
		})
		return payment.Status_STATUS_OK
	}

	// Step 3: Cancel the subscription
	// Configure cancellation parameters
	cancelParams := &stripe.SubscriptionCancelParams{
		// Optional: Specify when to cancel the subscription
		// InvoiceNow: stripe.Bool(true), // Generate a final invoice now
		// Prorate: stripe.Bool(true),    // Prorate the final invoice
	}

	// Cancel the subscription in Stripe
	subscription, err := subscription2.Cancel(stripeSubID, cancelParams)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":      "CancelSubscription",
			"subscriptionID": stripeSubID,
			"error":          "Failed to cancel subscription: " + err.Error(),
		})
		return payment.Status_STATUS_INTERNAL_ERROR
	}

	// Step 4: Verify the cancellation was successful
	if subscription.Status == stripe.SubscriptionStatusCanceled {
		p.log.PrintInfoWithContext(ctx, "Subscription cancelled successfully", map[string]string{
			"operation":      "CancelSubscription",
			"subscriptionID": stripeSubID,
			"status":         string(subscription.Status),
			"canceledAt":     time.Unix(subscription.CanceledAt, 0).Format(time.RFC3339),
		})
		return payment.Status_STATUS_OK
	} else {
		err := fmt.Errorf("subscription not cancelled, current status: %s", subscription.Status)
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":      "CancelSubscription",
			"subscriptionID": stripeSubID,
			"status":         string(subscription.Status),
		})
		return payment.Status_STATUS_INTERNAL_ERROR
	}
}

// GetSubscription retrieves detailed information about a subscription from Stripe
// This method handles the complete flow of retrieving subscription details:
// 1. Validate the subscription ID
// 2. Retrieve the subscription from Stripe
// 3. Extract and format the relevant information
func (p Payment) GetSubscription(ctx context.Context, stripeSubID string) data.Subscription {
	// Step 1: Validate subscription ID
	if stripeSubID == "" {
		err := fmt.Errorf("subscription ID is required")
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "GetSubscription",
		})
		return data.Subscription{}
	}

	p.log.PrintInfoWithContext(ctx, "Retrieving subscription details", map[string]string{
		"operation":      "GetSubscription",
		"subscriptionID": stripeSubID,
	})

	// Step 2: Retrieve the subscription from Stripe
	subscription, err := subscription2.Get(stripeSubID, nil)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":      "GetSubscription",
			"subscriptionID": stripeSubID,
			"error":          "Failed to retrieve subscription: " + err.Error(),
		})
		return data.Subscription{}
	}

	// Step 3: Check if subscription has items
	if len(subscription.Items.Data) == 0 {
		err := fmt.Errorf("subscription has no items")
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":      "GetSubscription",
			"subscriptionID": stripeSubID,
		})
		return data.Subscription{}
	}

	// Step 4: Create the subscription data object
	// Get the price ID from the first subscription item
	priceID := subscription.Items.Data[0].Price.ID

	// Set a default end date (current time + 30 days)
	endDate := time.Now().AddDate(0, 1, 0).Unix() // Default to 1 month from now

	result := data.Subscription{
		ID:               subscription.ID,
		PlanID:           priceID,
		StripeSubID:      stripeSubID,
		Status:           getPaymentStatusFromStripe(subscription.Status),
		CurrentPeriodEnd: endDate,
	}

	// Step 5: Log the retrieved subscription details
	p.log.PrintInfoWithContext(ctx, "Subscription details retrieved successfully", map[string]string{
		"operation":      "GetSubscription",
		"subscriptionID": stripeSubID,
		"status":         string(subscription.Status),
		"planID":         priceID,
		"periodEnd":      time.Unix(endDate, 0).Format(time.RFC3339),
	})

	return result
}

// handlePaymentIntent processes a payment intent for a subscription
// This is used to confirm payments or handle payment failures
func (p Payment) handlePaymentIntent(ctx context.Context, paymentIntentID string) error {
	p.log.PrintInfoWithContext(ctx, "Processing payment intent", map[string]string{
		"operation":       "handlePaymentIntent",
		"paymentIntentID": paymentIntentID,
	})

	// Retrieve the payment intent
	pi, err := paymentintent.Get(paymentIntentID, nil)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":       "handlePaymentIntent",
			"paymentIntentID": paymentIntentID,
			"error":           "Failed to retrieve payment intent: " + err.Error(),
		})
		return err
	}

	// Log payment intent status
	p.log.PrintInfoWithContext(ctx, "Payment intent status", map[string]string{
		"operation":       "handlePaymentIntent",
		"paymentIntentID": paymentIntentID,
		"status":          string(pi.Status),
	})

	// If payment intent requires confirmation, confirm it
	if pi.Status == stripe.PaymentIntentStatusRequiresConfirmation {
		p.log.PrintInfoWithContext(ctx, "Confirming payment intent", map[string]string{
			"operation":       "handlePaymentIntent",
			"paymentIntentID": paymentIntentID,
		})

		_, err = paymentintent.Confirm(paymentIntentID, nil)
		if err != nil {
			p.log.PrintErrorWithContext(ctx, err, map[string]string{
				"operation":       "handlePaymentIntent",
				"paymentIntentID": paymentIntentID,
				"error":           "Failed to confirm payment intent: " + err.Error(),
			})
			return err
		}
	}

	return nil
}

// getUserFromContext extracts the user ID from the context
func getUserFromContext(ctx context.Context) (int64, error) {
	val := ctx.Value(contextkeys.UserIDKey)
	if val == nil {
		return 0, status.Error(codes.Unauthenticated, "user id is missing in context")
	}

	userID, ok := val.(int64)
	if !ok {
		return 0, status.Error(codes.Unauthenticated, "user id is invalid in context")
	}

	return userID, nil
}

func getPaymentStatusFromStripe(subscriptionStatus stripe.SubscriptionStatus) payment.SubscriptionStatus {
	switch subscriptionStatus {
	case stripe.SubscriptionStatusActive:
		return payment.SubscriptionStatus_SUBSCRIPTION_STATUS_ACTIVE
	case stripe.SubscriptionStatusCanceled:
		return payment.SubscriptionStatus_SUBSCRIPTION_STATUS_CANCELED
	case stripe.SubscriptionStatusIncompleteExpired:
		return payment.SubscriptionStatus_SUBSCRIPTION_STATUS_INCOMPLETE_EXPIRED
	case stripe.SubscriptionStatusUnpaid:
		return payment.SubscriptionStatus_SUBSCRIPTION_STATUS_UNPAID
	case stripe.SubscriptionStatusTrialing:
		return payment.SubscriptionStatus_SUBSCRIPTION_STATUS_TRIALING
	default:
		return payment.SubscriptionStatus_SUBSCRIPTION_STATUS_UNSPECIFIED
	}
}

// mapPlanIDToStripePrice maps internal plan IDs to Stripe price IDs
func mapPlanIDToStripePrice(planID int32) string {
	switch planID {
	case 1:
		return "price_basic_123" // Basic plan price ID in Stripe
	case 2:
		return "price_pro_456"   // Pro plan price ID in Stripe
	default:
		return ""
	}
}

// getOrCreateCustomer retrieves an existing Stripe customer or creates a new one
// if the customer doesn't exist. This ensures we have a valid customer for the user.
func (p Payment) getOrCreateCustomer(ctx context.Context, userID string) (string, error) {
	p.log.PrintInfoWithContext(ctx, "Getting or creating Stripe customer", map[string]string{
		"operation": "getOrCreateCustomer",
		"userID":    userID,
	})

	// First, try to retrieve the customer by ID (assuming userID is used as customer ID)
	customerParams := &stripe.CustomerParams{}
	stripeCustomer, err := customer.Get(userID, customerParams)

	// If customer exists, return its ID
	if err == nil && stripeCustomer != nil {
		p.log.PrintInfoWithContext(ctx, "Retrieved existing Stripe customer", map[string]string{
			"operation":   "getOrCreateCustomer",
			"userID":      userID,
			"customerID":  stripeCustomer.ID,
		})
		return stripeCustomer.ID, nil
	}

	// Customer not found or other error, create a new one
	createParams := &stripe.CustomerParams{
		Description: stripe.String(fmt.Sprintf("Customer for user %s", userID)),
		Metadata: map[string]string{
			"user_id": userID,
		},
	}

	newCustomer, err := customer.New(createParams)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "getOrCreateCustomer",
			"userID":    userID,
			"error":     err.Error(),
		})
		return "", err
	}

	p.log.PrintInfoWithContext(ctx, "Created new Stripe customer", map[string]string{
		"operation":  "getOrCreateCustomer",
		"userID":     userID,
		"customerID": newCustomer.ID,
	})

	return newCustomer.ID, nil
}

// attachPaymentMethod attaches a payment method to a customer
// This is required before creating a subscription with the payment method
func (p Payment) attachPaymentMethod(ctx context.Context, customerID, paymentMethodID string) error {
	p.log.PrintInfoWithContext(ctx, "Attaching payment method to customer", map[string]string{
		"operation":       "attachPaymentMethod",
		"customerID":      customerID,
		"paymentMethodID": paymentMethodID,
	})

	// Attach payment method to customer
	params := &stripe.PaymentMethodAttachParams{
		Customer: stripe.String(customerID),
	}

	_, err := paymentmethod.Attach(paymentMethodID, params)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":       "attachPaymentMethod",
			"customerID":      customerID,
			"paymentMethodID": paymentMethodID,
			"error":           err.Error(),
		})
		return err
	}

	p.log.PrintInfoWithContext(ctx, "Payment method attached to customer successfully", map[string]string{
		"operation":       "attachPaymentMethod",
		"customerID":      customerID,
		"paymentMethodID": paymentMethodID,
	})

	return nil
}

// HandleStripeWebhook processes webhook events from Stripe
// This is crucial for handling asynchronous payment events like:
// - Payment successes and failures
// - Subscription updates and cancellations
// - Customer updates
// - Dispute and refund events
func (p Payment) HandleStripeWebhook(ctx context.Context, payload []byte, signature string, webhookSecret string) error {
	p.log.PrintInfoWithContext(ctx, "Processing Stripe webhook event", map[string]string{
		"operation": "HandleStripeWebhook",
	})

	// Verify the webhook signature
	event, err := stripe.ConstructEvent(payload, signature, webhookSecret)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation": "HandleStripeWebhook",
			"error":     "Failed to verify webhook signature: " + err.Error(),
		})
		return err
	}

	// Process different event types
	eventType := string(event.Type)

	switch eventType {
	case "payment_intent.succeeded":
		// Payment was successful
		p.log.PrintInfoWithContext(ctx, "Payment succeeded", map[string]string{
			"operation": "HandleStripeWebhook",
			"eventType": eventType,
			"eventID":   event.ID,
		})
		// In a real implementation, you would update your database to mark the payment as successful

	case "payment_intent.payment_failed":
		// Payment failed
		p.log.PrintWarnWithContext(ctx, "Payment failed", map[string]string{
			"operation": "HandleStripeWebhook",
			"eventType": eventType,
			"eventID":   event.ID,
		})
		// In a real implementation, you would notify the user and possibly retry the payment

	case "customer.subscription.created":
		// Subscription was created
		p.log.PrintInfoWithContext(ctx, "Subscription created", map[string]string{
			"operation": "HandleStripeWebhook",
			"eventType": eventType,
			"eventID":   event.ID,
		})
		// In a real implementation, you would update your database with the new subscription

	case "customer.subscription.updated":
		// Subscription was updated
		p.log.PrintInfoWithContext(ctx, "Subscription updated", map[string]string{
			"operation": "HandleStripeWebhook",
			"eventType": eventType,
			"eventID":   event.ID,
		})
		// In a real implementation, you would update your database with the subscription changes

	case "customer.subscription.deleted":
		// Subscription was deleted
		p.log.PrintInfoWithContext(ctx, "Subscription deleted", map[string]string{
			"operation": "HandleStripeWebhook",
			"eventType": eventType,
			"eventID":   event.ID,
		})
		// In a real implementation, you would update your database to mark the subscription as canceled

	default:
		// Log other events but don't process them
		p.log.PrintInfoWithContext(ctx, "Received unhandled Stripe event", map[string]string{
			"operation": "HandleStripeWebhook",
			"eventType": eventType,
			"eventID":   event.ID,
		})
	}

	return nil
}

// setDefaultPaymentMethod sets a payment method as the default for a customer
// This ensures future invoices use this payment method automatically
func (p Payment) setDefaultPaymentMethod(ctx context.Context, customerID, paymentMethodID string) error {
	p.log.PrintInfoWithContext(ctx, "Setting default payment method for customer", map[string]string{
		"operation":       "setDefaultPaymentMethod",
		"customerID":      customerID,
		"paymentMethodID": paymentMethodID,
	})

	// Update customer with default payment method
	params := &stripe.CustomerParams{
		InvoiceSettings: &stripe.CustomerInvoiceSettingsParams{
			DefaultPaymentMethod: stripe.String(paymentMethodID),
		},
	}

	_, err := customer.Update(customerID, params)
	if err != nil {
		p.log.PrintErrorWithContext(ctx, err, map[string]string{
			"operation":       "setDefaultPaymentMethod",
			"customerID":      customerID,
			"paymentMethodID": paymentMethodID,
			"error":           err.Error(),
		})
		return err
	}

	p.log.PrintInfoWithContext(ctx, "Default payment method set successfully", map[string]string{
		"operation":       "setDefaultPaymentMethod",
		"customerID":      customerID,
		"paymentMethodID": paymentMethodID,
	})

	return nil
}
