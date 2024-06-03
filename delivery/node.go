package delivery

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	deliveryProto "github.com/go-preform/kitchen/delivery/protobuf"
	zmq "github.com/go-zeromq/zmq4"
	"google.golang.org/protobuf/proto"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type node struct {
	server               *server
	orderLock            sync.Mutex
	reqSocketLock        sync.Mutex
	reqSocket            sync.Pool
	handleOrderIdPtr     *uint64
	handleOrderCancelers []context.CancelFunc
	orderResponses       []orderWaiter
	pushMessageCh        chan []byte
	pushAckCh            chan *bytes.Buffer
	url                  string
	status               *deliveryProto.NodeStatus
	reqSocketInUse       *int32
	reqSocketInPool      *int32
	ringLimit            uint64
	Id                   uint32
	reqSocketMax         int32
	reqSocketMaxIdle     int32
}

type orderWaiter struct {
	sync.Mutex
	responseChs []chan *deliveryProto.Deliverable
	orderIds    []uint64
}

func newNode(server *server, url string, repPort uint16, localId uint32, reqSocketMax int32) *node {
	n := &node{
		reqSocketInPool:  new(int32),
		reqSocketInUse:   new(int32),
		reqSocketMax:     reqSocketMax,
		reqSocketMaxIdle: reqSocketMax / 2,
		server:           server,
		url:              url,
		Id:               localId,
		reqSocket: sync.Pool{
			New: func() interface{} {
				return nil
			},
		},
		status: &deliveryProto.NodeStatus{
			Host:    url,
			RepPort: uint32(repPort),
			NodeId:  localId,
		},
	}
	return n
}

func (n *node) id() uint32 {
	return n.Id
}

func (n *node) getReqSock() (zmq.Socket, error) {
	if n.status.Offline {
		return nil, ErrOffline
	}
	r := n.reqSocket.Get()
	if r == nil {
		n.reqSocketLock.Lock()
		if atomic.AddInt32(n.reqSocketInUse, 1) < n.reqSocketMax {
			n.reqSocketLock.Unlock()
		} else {
			LogDebug("reqSocketInUse lock", nil)
		}
		rr := zmq.NewReq(n.server.ctx, zmq.WithAutomaticReconnect(true), zmq.WithTimeout(DefaultOrderTimeout), zmq.WithDialerTimeout(DefaultOrderTimeout), zmq.WithDialerMaxRetries(3))
		//LogErr("Dial", nil, n.server.nodeId, n.Id, fmt.Sprintf("%s:%d", n.url, n.status.RepPort))
		err := rr.Dial(fmt.Sprintf("%s:%d", n.url, n.status.RepPort))
		if err != nil {
			LogErr("Connect", err, n.server.nodeId)
			atomic.AddInt32(n.reqSocketInUse, -1)
			return nil, err
		}
		return rr, nil
	}
	atomic.AddInt32(n.reqSocketInPool, -1)
	return r.(zmq.Socket), nil
}

func (n *node) cleanReqSock(r zmq.Socket) {
	atomic.AddInt32(n.reqSocketInPool, 1)
	n.reqSocket.Put(r)
	if atomic.AddInt32(n.reqSocketInUse, -1) == n.reqSocketMax-1 {
		n.reqSocketLock.Unlock()
	}
}

func (n *node) initRegister(retry uint32) {
	var (
		err error
	)
	if retry != 0 {
		if retry > 100 {
			retry = 100
		}
		time.Sleep(time.Millisecond * 100 * time.Duration(retry))
	}
	n.ringLimit = uint64(DefaultOrderHandleConcurrentLimit * 1000)
	n.handleOrderCancelers = make([]context.CancelFunc, n.ringLimit)
	n.orderResponses = make([]orderWaiter, n.ringLimit)
	n.handleOrderIdPtr = new(uint64)
	n.status.Host = n.url
	LogDebug("connect", n.url, n.status.RepPort)
	r, err := n.getReqSock()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			LogDebug("server shutdown", n.url, n.status.RepPort)
			return
		}
		LogErr("Connect rep", err)
		n.initRegister(retry + 1)
		return
	}
	defer n.cleanReqSock(r)
	if n.server.nodeId == 0 {
		data := n.connectToHost(retry)
		if len(data) < 5 || data[0] != MSG_FLAG_CONNECTED_ACK {
			LogErr("node invalid ack", nil, data)
			n.initRegister(retry + 1)
			return
		}
		n.server.nodeId = binary.BigEndian.Uint32(data[1:5])
		n.status.NodeId = 1
		chainStatus := &deliveryProto.ChainStatus{}
		err = proto.Unmarshal(data[5:], chainStatus)
		if err != nil {
			LogErr("node initRegister Unmarshal", err)
			return
		}
		n.server.checkNodes(chainStatus, n)
	}
	go n.pushMessageAndCleanConnLoop()
}

func (n *node) connectToHost(retry uint32) []byte {
	if retry != 0 {
		if retry > 100 {
			retry = 100
		}
		time.Sleep(time.Millisecond * 100 * time.Duration(retry))
	}
	var (
		reqData    = make([]byte, 1)
		statusData []byte
		err        error
		r          zmq.Socket
	)
	nodeStatus := *n.server.status
	nodeStatus.SendTime = time.Now().UnixMicro()
	statusData, _ = proto.Marshal(&nodeStatus)
	r, err = n.getReqSock()
	err = r.Send(zmq.NewMsg(append(reqData, statusData...)))
	if err != nil {
		n.server.onSendErr(n.Id, err)
		LogErr("node SendBytes", err)
		return n.connectToHost(retry + 1)
	}
	LogDebug("wait", n.url)
	resp, err := r.Recv()
	if err != nil {
		LogErr("node RecvBytes", err)
		return n.connectToHost(retry + 1)
	}
	return resp.Bytes()
}

var (
	ErrOffline         = errors.New("offline")
	ErrInvalidResponse = errors.New("invalid response")
	ErrOrderAckTimeout = errors.New("order ack timeout")
	ErrOrderTimeout    = errors.New("order timeout")
	ErrMenuNotServing  = errors.New("menu not serving")
)

var (
	orderHeader  = []byte{MSG_FLAG_ORDER}
	orderMsgPool = sync.Pool{
		New: func() interface{} {
			return &deliveryProto.Order{}
		},
	}
	orderDataBufferPool = sync.Pool{
		New: func() any {
			return bytes.NewBuffer(orderHeader)
		},
	}
	responseDataBufferPool = sync.Pool{
		New: func() any {
			return bytes.NewBuffer([]byte{MSG_FLAG_ORDER_RESULT})
		},
	}
)

func (n *node) OrderRequestAsClient(ctx context.Context, menuId, dishId uint16, input []byte) (output []byte, err error) {
	var (
		resCh     chan *deliveryProto.Deliverable
		waiter    *orderWaiter
		returnErr = ErrOrderAckTimeout
		deadline  = time.Now().Add(DefaultOrderTimeout)
	)
	ackTimeout := time.NewTimer(DefaultOrderAckTimeout)
	req := orderMsgPool.Get().(*deliveryProto.Order) //&deliveryProto.Order{}
	req.MenuId = uint32(menuId)
	req.DishId = uint32(dishId)
	req.NodeId = n.server.nodeId
	req.Deadline = deadline.UnixMicro()
	req.Input = input
	req.Id = atomic.AddUint64(n.handleOrderIdPtr, 1) % n.ringLimit
	resCh = make(chan *deliveryProto.Deliverable, 1) //sometimes ack comeback later so buffer can prevent blocking
	waiter = &n.orderResponses[req.Id]
	waiter.Lock()
	waiter.responseChs = append(waiter.responseChs, resCh)
	waiter.orderIds = append(waiter.orderIds, req.Id)
	waiter.Unlock()
	data, err := proto.Marshal(req)
	//traceRing[req.Id*10+uint64(req.NodeId)] = 1
	if err != nil {
		LogErr("node OrderRequestAsClient Marshal", err)
		return
	}
	LogDebug("send req")
	orderDataBuffer := orderDataBufferPool.Get().(*bytes.Buffer)
	orderDataBuffer.Write(data)
	r, err := n.getReqSock()
	if err != nil {
		LogErr("node OrderRequestAsClient getReqSock", err)
		return
	}
	err = r.Send(zmq.NewMsg(orderDataBuffer.Bytes()))
	if err != nil {
		LogErr("node OrderRequestAsClient Send", err)
		return
	}
	n.cleanReqSock(r)
	orderDataBuffer.Truncate(1)
	orderDataBufferPool.Put(orderDataBuffer)
	for {
		select {
		case outputResp, ok := <-resCh:
			if !ok {
				return nil, ErrOrderAckTimeout
			}
			if outputResp == nil {
				//atomic.AddUint32(&traceRing[req.Id*10+uint64(n.Id)], 64)
				ackTimeout.Reset(deadline.Sub(time.Now()))
				returnErr = ErrOrderTimeout
			} else {
				//atomic.AddUint32(&traceRing[req.Id*10+uint64(req.NodeId)], 128)
				if outputResp.Error != "" {
					deliverableWrapPool.Put(outputResp)
					req.Reset()
					orderMsgPool.Put(req)
					return nil, errors.New(outputResp.Error)
				}
				data = outputResp.Output
				deliverableWrapPool.Put(outputResp)
				req.Reset()
				orderMsgPool.Put(req)
				return data, nil
			}
		case <-ackTimeout.C:
			goto cancel
		case <-ctx.Done():
			returnErr = ctx.Err()
			goto cancel
		}
	}
cancel:
	for i, orderId := range waiter.orderIds {
		if orderId == req.Id {
			waiter.Lock()
			waiter.responseChs[0], waiter.responseChs[i] = waiter.responseChs[i], waiter.responseChs[0]
			waiter.orderIds[0], waiter.orderIds[i] = waiter.orderIds[i], waiter.orderIds[0]
			waiter.responseChs = waiter.responseChs[1:]
			waiter.orderIds = waiter.orderIds[1:]
			waiter.Unlock()
			break
		}
	}
	r, err = n.getReqSock()
	if err != nil {
		LogErr("node OrderRequestAsClient Cancel getReqSock", err)
		return nil, err
	}
	data = make([]byte, 13)
	data[0] = MSG_FLAG_ORDER_CANCEL
	binary.BigEndian.PutUint32(data[1:5], n.server.nodeId)
	binary.BigEndian.PutUint64(data[5:], req.Id)
	err = r.Send(zmq.NewMsg(data))
	if err != nil {
		LogErr("node OrderRequestAsClient Cancel", err)
		return nil, err
	}
	req.Reset()
	orderMsgPool.Put(req)
	return nil, returnErr
}

var (
	orderAckPool = sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer([]byte{MSG_FLAG_ORDER_ACK})
		},
	}
	deliverablePool = sync.Pool{
		New: func() interface{} {
			return &deliveryProto.Deliverable{}
		},
	}
)

func (n *node) orderIdAndResponseFn(orderId uint64, deadline time.Time) (ctx context.Context, ackFn func(), responseFn func(data []byte, err error) error) {
	//LogErr("orderIdAndResponseFn", nil, n.server.nodeId, n.Id, orderId)
	ctx, n.handleOrderCancelers[orderId] = context.WithDeadline(n.server.ctx, deadline)
	return ctx, func() {
			data := orderAckPool.Get().(*bytes.Buffer)
			_ = binary.Write(data, binary.BigEndian, n.server.nodeId)
			_ = binary.Write(data, binary.BigEndian, orderId)
			n.pushAckCh <- data
			//atomic.AddUint32(&traceRing[orderId*10+uint64(n.Id)], 4)
		}, func(data []byte, err error) error {
			resp := deliverablePool.Get().(*deliveryProto.Deliverable)
			resp.OrderId = orderId
			resp.NodeId = n.server.nodeId
			resp.Output = data
			if err != nil {
				resp.Error = err.Error()
			}
			data, _ = proto.Marshal(resp)
			responseDataBuffer := responseDataBufferPool.Get().(*bytes.Buffer)
			responseDataBuffer.Write(data)
			//atomic.AddUint32(&traceRing[orderId*10+uint64(n.Id)], 32)
			//toDeliverPool.Put(resp)
			LogDebug("send outputResp")
			r, err := n.getReqSock()
			if err != nil {
				LogErr("Connect", err)
				return err
			}
			defer func() {
				n.cleanReqSock(r)
				responseDataBuffer.Truncate(1)
				responseDataBufferPool.Put(responseDataBuffer)
			}()
			return r.Send(zmq.NewMsg(responseDataBuffer.Bytes()))
		}

}

func (n *node) pushMessageAndRecv(bytes []byte) ([]byte, error) {
	r, err := n.getReqSock()
	if err != nil {
		return nil, err
	}
	defer n.cleanReqSock(r)
	LogDebug("send", n.Id, bytes)
	err = r.Send(zmq.NewMsg(bytes))
	if err != nil {
		return nil, err
	}
	resp, err := r.Recv()
	if err != nil {
		return nil, err
	}
	return resp.Bytes(), nil
}

func (n *node) pushMessage(bytes []byte) error {
	r, err := n.getReqSock()
	if err != nil {
		return err
	}
	defer n.cleanReqSock(r)
	err = r.Send(zmq.NewMsg(bytes))
	if err != nil {
		n.server.onSendErr(n.Id, err)
	}
	return err
}

func (n *node) pushMessageAndCleanConnLoop() {
	n.pushAckCh = make(chan *bytes.Buffer, 100)
	n.pushMessageCh = make(chan []byte, 100)
	var (
		err             error
		cleanConnTicker = time.NewTicker(DefaultOrderTimeout)
		msg             []byte
		buff            *bytes.Buffer
		ok              bool
		closed          bool
		toClose         []zmq.Socket
		pl              int32
		req             zmq.Socket
	)
	req, err = n.getReqSock()
	if err != nil {
		LogErr("pushMessageAndCleanConnLoop getReqSock", err)
		return
	}
	for {
		select {
		case <-n.server.ctx.Done():
			if !closed {
				closed = true
				cleanConnTicker.Stop()
				close(n.pushMessageCh)
				for atomic.AddInt32(n.reqSocketInPool, -1) > 0 {
					r := n.reqSocket.Get()
					if r == nil {
						break
					}
					_ = r.(zmq.Socket).Close()
				}
			}
		case <-cleanConnTicker.C:
			for _, r := range toClose {
				_ = r.Close()
			}
			toClose = toClose[:0]
			if pl = atomic.LoadInt32(n.reqSocketInPool); pl > n.reqSocketMaxIdle {
				for pl > n.reqSocketMaxIdle {
					r := n.reqSocket.Get()
					if r == nil {
						break
					}
					toClose = append(toClose, r.(zmq.Socket))
					pl = atomic.AddInt32(n.reqSocketInPool, -1)
				}
			}
		case buff, ok = <-n.pushAckCh:
			if !ok {
				return
			}
			err = req.Send(zmq.NewMsg(buff.Bytes()))
			buff.Truncate(1)
			orderAckPool.Put(buff)
		case msg, ok = <-n.pushMessageCh:
			if !ok {
				return
			}
			err = req.Send(zmq.NewMsg(msg))
			if err != nil {
				LogErr("pushMessageAndCleanConnLoop", err)
			}
		}
	}
}

func (n *node) calcLeaderRank() int {
	if n.Id == 0 || n.status.Offline {
		return math.MaxInt64
	}
	return int(n.status.MemoryMB) + int(n.status.CpuCore*512) - int(n.status.SendTime*10) - int(n.status.FailCount*100)
}

func (n *node) calcWorkLoad() int {
	adj := int(n.status.Loading*20) + int(n.status.ProcessedInMinute)
	if n.status.CpuUsage != 0 {
		adj = (adj * int(n.status.CpuUsage)) / 100
	}
	return adj
}
