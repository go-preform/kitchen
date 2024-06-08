package delivery

import (
	"context"
	deliveryProto "github.com/go-preform/kitchen/delivery/protobuf"
)

type Order struct {
	Response func([]byte, error) error
	Ack      func()
	Ctx      context.Context
	deliveryProto.Order
}

type Deliverable struct {
	Output  []byte
	Error   error
	OrderId uint64
}

// ILogistic is an interface of managing scaling of kitchen.
type ILogistic interface {
	Init(ctx context.Context) error
	SetOrderHandlerPerMenu([]func(context.Context, *Order))
	SwitchLeader(url string, port uint16)
	// Shutdown is for shutting down the logistic server keep every call in local
	Shutdown()
	IsLeader() bool
	// Order is for sending orders to cluster
	Order(menuId, dishId uint16, skipNodeIds ...uint32) (func(ctx context.Context, input []byte) ([]byte, error), error)
}

type LogisticOpt struct {
	disableMasterElection bool
}

func OptMasterElectionOnDrop(on bool) iLogisticOptSetter {
	return func(opt *LogisticOpt) {
		opt.disableMasterElection = !on
	}
}

type iLogisticOptSetter func(*LogisticOpt)
