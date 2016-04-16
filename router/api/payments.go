package api

import (
	"gopkg.in/macaron.v1"
)

type PlansResp struct {
	Resp
	Plans []*Plan
}

type Plan struct {
	ID string
	Type int
	Cycle int
	Name string
	Currency string
	Amount int
	MaxDomains int
	MaxAddresses int
	MaxSpace int
	MaxMembers int
	TwoFactor int
}

func (api *Api) GetPlans(ctx *macaron.Context) {
	ctx.JSON(200, &PlansResp{
		Resp: Resp{Ok},
		Plans: []*Plan{},
	})
}

type Subscription struct {
	Plan
	Plans []*Plan
}

type SubscriptionResp struct {
	Resp
	Subscription *Subscription
}

func (api *Api) GetSubscription(ctx *macaron.Context) {
	ctx.JSON(200, &SubscriptionResp{
		Resp: Resp{Ok},
		Subscription: &Subscription{},
	})
}

type PaymentMethodsResp struct {
	Resp
	PaymentMethods []interface{} // TODO
}

func (api *Api) GetPaymentMethods(ctx *macaron.Context) {
	ctx.JSON(200, &PaymentMethodsResp{
		Resp: Resp{Ok},
		PaymentMethods: []interface{}{},
	})
}

type InvoicesResp struct {
	Resp
	Invoices []interface{} // TODO
}

func (api *Api) GetInvoices(ctx *macaron.Context) {
	ctx.JSON(200, &InvoicesResp{
		Resp: Resp{Ok},
		Invoices: []interface{}{},
	})
}
