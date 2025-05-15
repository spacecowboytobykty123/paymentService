package payment

import (
	"context"
	"github.com/spacecowboytobykty123/paymentProto/gen/go/payment"
	"github.com/stripe/stripe-go/v82"
	subscription2 "github.com/stripe/stripe-go/v82/subscription"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	contextkeys "paymentService/internal/contextkey"
	"paymentService/internal/data"
	"paymentService/internal/jsonlog"
	"strconv"
	"time"
)

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

func (p Payment) CreateSubscription(ctx context.Context, planID int32, paymentMethod string) (string, payment.Status) {
	userId, err := getUserFromContext(ctx)
	if err != nil {
		return "", payment.Status_STATUS_INVALID_USER
	}
	clearUserId := strconv.FormatInt(userId, 10)
	params := &stripe.SubscriptionParams{
		Customer: stripe.String(clearUserId),
		Items: []*stripe.SubscriptionItemsParams{
			{
				Price: stripe.String(mapPlanIDToStripePrice(planID)),
			},
		},
	}
	params.AddExpand("latest_invoice.payment_intent")

	subscription, err := subscription2.New(params)
	if err != nil {
		return "", payment.Status_STATUS_INTERNAL_ERROR
	}

	return subscription.ID, payment.Status_STATUS_OK
}

func (p Payment) CancelSubscription(ctx context.Context, stripeSubID string) payment.Status {
	subscription, err := subscription2.Cancel(stripeSubID, nil)
	if err != nil {
		return payment.Status_STATUS_INTERNAL_ERROR
	}

	if subscription.Status == stripe.SubscriptionStatusCanceled {
		return payment.Status_STATUS_OK
	} else {
		return payment.Status_STATUS_INTERNAL_ERROR
	}

}

func (p Payment) GetSubscription(ctx context.Context, stripeSubID string) data.Subscription {
	subscription, err := subscription2.Get(stripeSubID, nil)
	if err != nil {
		return data.Subscription{}
	}

	return data.Subscription{
		ID:               subscription.ID,
		PlanID:           subscription.Items.Data[0].Price.ID,
		Status:           getPaymentStatusFromStripe(subscription.Status),
		CurrentPeriodEnd: subscription.EndedAt,
	}
}

func getUserFromContext(ctx context.Context) (int64, error) {
	println("getUserFromContext")
	val := ctx.Value(contextkeys.UserIDKey)
	userID, ok := val.(int64)
	if !ok {
		return 0, status.Error(codes.Unauthenticated, "user id is missing or invalid in context")
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

func mapPlanIDToStripePrice(planID int32) string {
	switch planID {
	case 1:
		return "price_basic_123"
	case 2:
		return "price_pro_456"
	default:
		return ""
	}
}
