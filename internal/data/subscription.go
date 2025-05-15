package data

import "github.com/spacecowboytobykty123/paymentProto/gen/go/payment"

type Subscription struct {
	ID               string
	PlanID           string
	StripeSubID      string
	Status           payment.SubscriptionStatus
	CurrentPeriodEnd int64
}
