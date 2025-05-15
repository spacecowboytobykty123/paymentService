package payment

import (
	"context"
	"github.com/spacecowboytobykty123/paymentProto/gen/go/payment"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"paymentService/internal/data"
	"strconv"
)

type serverAPI struct {
	payment.UnimplementedPaymentServiceServer
	payment Payment
}

type Payment interface {
	CreateSubscription(ctx context.Context, planID int32, paymentMethod string) (string, payment.Status)
	CancelSubscription(ctx context.Context, stripeSubID string) payment.Status
	GetSubscription(ctx context.Context, stripeSubID string) data.Subscription
}

func Register(gRPC *grpc.Server, pay Payment) {
	payment.RegisterPaymentServiceServer(gRPC, &serverAPI{payment: pay})
}

func (s *serverAPI) CreateSubscription(ctx context.Context, r *payment.CreateSubscriptionRequest) (*payment.CreateSubscriptionResponse, error) {
	planID := r.GetPlanId()
	paymentMethod := r.GetPaymentMethodId()

	stripeSubId, opStatus := s.payment.CreateSubscription(ctx, planID, paymentMethod)

	if opStatus != payment.Status_STATUS_OK {
		return nil, mapStatusToError(opStatus)
	}

	return &payment.CreateSubscriptionResponse{
		SubStripeId: stripeSubId,
		Status:      opStatus,
	}, nil
}

func (s *serverAPI) CancelSubscription(ctx context.Context, r *payment.CancelSubscriptionRequest) (*payment.CancelSubscriptionResponse, error) {
	stripeSubID := r.GetSubStripeId()

	opStatus := s.payment.CancelSubscription(ctx, stripeSubID)
	if opStatus != payment.Status_STATUS_OK {
		return nil, mapStatusToError(opStatus)
	}

	return &payment.CancelSubscriptionResponse{Status: opStatus}, nil
}

func (s *serverAPI) GetSubscription(ctx context.Context, r *payment.GetSubscriptionRequest) (*payment.GetSubscriptionResponse, error) {
	stripeSubID := r.GetSubStripeId()

	subscription := s.payment.GetSubscription(ctx, stripeSubID)

	subId, err := strconv.ParseInt(subscription.ID, 10, 64)
	if err != nil {
		return nil, err
	}
	planID, err := strconv.ParseInt(subscription.PlanID, 10, 32)
	return &payment.GetSubscriptionResponse{Subscription: &payment.Subscription{
		Id:                   subId,
		PlanId:               int32(planID),
		StripeSubscriptionId: stripeSubID,
		Status:               subscription.Status,
		CurrentPeriodEnd:     subscription.CurrentPeriodEnd,
	}}, nil

}

func mapStatusToError(opstatus payment.Status) error {
	switch opstatus {
	case payment.Status_STATUS_INVALID_USER:
		return status.Error(codes.InvalidArgument, "Invalid user")
	case payment.Status_STATUS_INVALID_PLAN:
		return status.Error(codes.InvalidArgument, "invalid plan")
	case payment.Status_STATUS_INVALID_PAYMENT_METHOD:
		return status.Error(codes.InvalidArgument, "invalid paying method")
	default:
		return status.Error(codes.Internal, "internal error!")

	}
}
