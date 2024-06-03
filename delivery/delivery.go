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

type ILogistic interface {
	Init() error
	SetOrderHandlerPerMenu([]func(context.Context, *Order))
	SwitchLeader(url string, port uint16)
	Shutdown()
	IsLeader() bool
	Order(menuId, dishId uint16, skipNodeIds ...uint32) (func(ctx context.Context, input []byte) ([]byte, error), error)
}
