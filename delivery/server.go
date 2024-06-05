package delivery

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	deliveryProto "github.com/go-preform/kitchen/delivery/protobuf"
	zmq "github.com/go-zeromq/zmq4"
	"github.com/mackerelio/go-osstat/cpu"
	"github.com/pbnjay/memory"
	"google.golang.org/protobuf/proto"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	DefaultListenAddr                 = "0.0.0.0"
	DefaultPubPort                    = 19527
	DefaultRepPort                    = 29527
	DefaultOrderHandleConcurrentLimit = 500
	DefaultReqSocketMax               = int32(100)
	DefaultOrderTimeout               = time.Second * 10
	DefaultOrderAckTimeout            = time.Second * 3
	DefaultStatusBroadcastInterval    = time.Second * 1
	LogErr                            = func(str string, err error, arg ...interface{}) {
		fmt.Println(str, err, arg)
	}
	LogInfo = func(args ...interface{}) {
		fmt.Println(args...)
	}
	LogDebug = func(args ...interface{}) {
		//fmt.Println(args...)
	}
)

const (
	MSG_FLAG_CONNECTED        byte = 0
	MSG_FLAG_CONNECTED_ACK    byte = 1
	MSG_FLAG_ORDER            byte = 2
	MSG_FLAG_ORDER_ACK        byte = 3
	MSG_FLAG_ORDER_CANCEL     byte = 4
	MSG_FLAG_ORDER_RESULT     byte = 5
	MSG_FLAG_STATUS_SUBMIT    byte = 6
	MSG_FLAG_STATUS_BOARDCASE byte = 7
	MSG_FLAG_STATUS_PING      byte = 8
	MSG_FLAG_STATUS_INHERIT   byte = 9
)

// server is the default implementation of ILogistic with async request handling.
type server struct {
	sync.RWMutex
	ctx                 context.Context
	ctxCancel           context.CancelFunc
	handleCtx           context.Context
	handleCtxCancel     context.CancelFunc
	pushStatusCtxCancel context.CancelFunc
	pushStatusCh        chan struct{}
	hostCtx             context.Context
	hostCtxCancel       context.CancelFunc
	hostHeartbeat       *time.Timer
	orderListener       []func(context.Context, *Order)
	orderServerByMenu   []ILoadBalancer
	recvStatusCh        chan []byte
	cancelListen        context.CancelFunc
	localUrl            string
	mainUrl             string
	orderCh             chan *Order
	orderDataCh         chan []byte
	status              *deliveryProto.NodeStatus
	peers               []*node
	peersByUrl          map[string]*node
	leaderRank          []uint32
	loadingPtr          *int32
	callStatForSecond   *uint32
	nodeId              uint32
	localRepPort        uint16
	hostRepPort         uint16
}

func (s *server) SwitchHost(host string) {
	s.mainUrl = host
	if s.hostCtxCancel != nil {
		s.hostCtxCancel()
		s.Lock()
		s.peers = nil
		s.Unlock()
	}
	if s.pushStatusCtxCancel != nil {
		s.pushStatusCtxCancel()
	}
	n := newNode(s, s.mainUrl, s.hostRepPort, 1, DefaultReqSocketMax)
	n.initRegister(0)
	if s.nodeId != 1 {
		go s.pushStatusLoop()
	}
}

func NewServer(localHostUrl string, localRepPort uint16, mainHostUrl string, hostRepPort uint16) ILogistic {
	s := &server{
		localUrl:     localHostUrl,
		mainUrl:      mainHostUrl,
		peersByUrl:   make(map[string]*node),
		localRepPort: localRepPort,
		hostRepPort:  hostRepPort,
		status: &deliveryProto.NodeStatus{
			RepPort: uint32(localRepPort),
			Host:    localHostUrl,
		},
	}
	s.loadingPtr = &s.status.Loading
	return s
}

func (s *server) Shutdown() {
	s.ctxCancel()
}

func (s *server) Init() error {
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())
	s.status.MemoryMB = uint32(memory.TotalMemory() / 1024 / 1024)
	s.status.CpuCore = uint32(runtime.NumCPU())
	if s.mainUrl == "" {
		LogInfo("main host", s.localUrl)
		s.asMainHost()
		s.peers = append(s.peers, newNode(s, s.localUrl, s.localRepPort, 1, DefaultReqSocketMax))
		s.peers[0].status.MemoryMB = s.status.MemoryMB
		s.peers[0].status.CpuCore = s.status.CpuCore
		s.peers[0].status.ServeMenuIds = s.status.ServeMenuIds
		s.status = s.peers[0].status
		s.nodeId = 1
	} else {
		LogInfo("sub host", s.localUrl, s.mainUrl)
		s.SwitchHost(s.mainUrl)
	}
	if s.orderListener != nil {
		for i := range s.orderListener {
			s.orderServerByMenu[i] = NewLoadBalancer(s.nodeId)
		}
	}
	err := s.listenRequest()
	if err != nil {
		return err
	}
	go s.handleOrders()
	return nil
}

func (s *server) IsLeader() bool {
	return s.hostCtx != nil && s.hostCtx.Err() == nil
}

func (s *server) listenRequest() error {
	var (
		err error
		url = s.localUrl
	)
	if url == "" {
		url = fmt.Sprintf("tcp://%s", DefaultListenAddr)
	}
	s.status.Host = url
	url = fmt.Sprintf("%s:%d", url, s.localRepPort)
	responder := zmq.NewRep(s.ctx)
	if err != nil {
		return err
	}
	err = responder.Listen(url)
	if err != nil {
		return err
	}
	s.orderCh = make(chan *Order, DefaultOrderHandleConcurrentLimit*10)
	s.orderDataCh = make(chan []byte, DefaultOrderHandleConcurrentLimit*10)
	_, s.cancelListen = context.WithCancel(context.Background())
	go func() {
		var (
			n              *node
			reqData        []byte
			msg            zmq.Msg
			order          *Order
			orderMsg       = &deliveryProto.Order{}
			deliverableMsg *deliveryProto.Deliverable
			nodeId         uint32
			thisOrderId    uint64
			i              int
			orderId        uint64
			fn             context.CancelFunc
			waiter         *orderWaiter
			chainStatus    = &deliveryProto.ChainStatus{}
		)
		LogDebug("server start", url)
		for {
			msg, err = responder.Recv()
			if err != nil {
				if errors.Is(context.Canceled, err) {
					_ = responder.Close()
					LogDebug("server shutdown", s.nodeId)
					return
				}
				LogErr("Recv order", err)
			}
			reqData = msg.Bytes()
			//logDebug("received", reqData)
			switch reqData[0] {
			case MSG_FLAG_CONNECTED: // connect
				err = responder.Send(zmq.NewMsg(s.addNodeAndReply(reqData[1:])))
				if err != nil {
					LogErr("Send connected ack", err)
				}
			case MSG_FLAG_ORDER: // order
				err = proto.Unmarshal(reqData[1:], orderMsg)
				if err != nil {
					LogErr("Unmarshal order", err)
					continue
				}
				order = orderWrapPool.Get().(*Order)
				order.Input = orderMsg.Input
				order.DishId = orderMsg.DishId
				order.MenuId = orderMsg.MenuId

				order.Id = orderMsg.Id         // not needed
				order.NodeId = orderMsg.NodeId // not needed
				//atomic.AddUint32(&traceRing[orderMsg.Id*10+uint64(orderMsg.NodeId)], 2)

				orderMsg.NodeId--
				if len(s.peers) > int(orderMsg.NodeId) {
					order.Ctx, order.Ack, order.Response = s.peers[orderMsg.NodeId].orderIdAndResponseFn(orderMsg.Id, time.UnixMicro(orderMsg.Deadline))
				} else {
					LogErr("node not found", nil, orderMsg.NodeId)
					continue
				}
				atomic.AddInt32(s.loadingPtr, 1)
				s.orderCh <- order
			case MSG_FLAG_ORDER_ACK:
				nodeId = binary.BigEndian.Uint32(reqData[1:5]) - 1
				thisOrderId = binary.BigEndian.Uint64(reqData[5:])
				if len(s.peers) > int(nodeId) {
					n = s.peers[nodeId]
					if waiter = &n.orderResponses[thisOrderId]; len(waiter.orderIds) != 0 {
						for i, orderId = range waiter.orderIds {
							if orderId == thisOrderId {
								waiter.Lock()
								waiter.responseChs[i] <- nil
								waiter.Unlock()
								break
							}
						}
					}
				} else {
					LogErr("node not found", nil)
				}
			case MSG_FLAG_ORDER_CANCEL: // cancel
				if len(reqData) < 13 {
					LogErr("invalid cancel", nil)
					continue
				}
				nodeId = binary.BigEndian.Uint32(reqData[1:5]) - 1
				thisOrderId = binary.BigEndian.Uint64(reqData[5:13])
				if len(s.peers) > int(nodeId) {
					n = s.peers[nodeId]
					if fn = n.handleOrderCancelers[thisOrderId]; fn != nil {
						fn()
						n.handleOrderCancelers[thisOrderId] = nil
					}
				}
			case MSG_FLAG_ORDER_RESULT: // result
				deliverableMsg = deliverableWrapPool.Get().(*deliveryProto.Deliverable) //&deliveryProto.Deliverable{}
				err = proto.Unmarshal(reqData[1:], deliverableMsg)
				if err != nil {
					LogErr("Unmarshal deliverable", err)
					continue
				}
				deliverableMsg.NodeId--
				if uint32(len(s.peers)) > deliverableMsg.NodeId {
					n = s.peers[deliverableMsg.NodeId]
					if waiter = &n.orderResponses[deliverableMsg.OrderId]; len(waiter.orderIds) != 0 {
						for i, orderId = range waiter.orderIds {
							if orderId == deliverableMsg.OrderId {
								waiter.Lock()
								waiter.responseChs[i] <- deliverableMsg
								waiter.responseChs[i], waiter.responseChs[0] = waiter.responseChs[0], waiter.responseChs[i]
								waiter.orderIds[i], waiter.orderIds[0] = waiter.orderIds[0], waiter.orderIds[i]
								waiter.responseChs = waiter.responseChs[1:]
								waiter.orderIds = waiter.orderIds[1:]
								waiter.Unlock()
								break
							}
						}
					} else {
						LogDebug("orderResponseCh closed")
					}
				} else {
					LogErr("node not found", nil)
				}
			case MSG_FLAG_STATUS_SUBMIT: // status
				s.recvStatusCh <- reqData[1:]
			case MSG_FLAG_STATUS_BOARDCASE: // status
				err = proto.Unmarshal(reqData[1:], chainStatus)
				if err != nil {
					LogErr("Unmarshal chain status", err)
					continue
				}
				//LogErr("got push status", nil, s.nodeId)
				s.checkNodes(chainStatus, nil)
			case MSG_FLAG_STATUS_PING:
				err = responder.Send(zmq.NewMsg([]byte{MSG_FLAG_STATUS_PING}))
				if err != nil {
					LogErr("Send ping ack", err)
				}
			case MSG_FLAG_STATUS_INHERIT:
				if s.hostCtx != nil && s.hostCtx.Err() == nil {
					err = responder.Send(zmq.NewMsg(append([]byte{1})))
					if err != nil {
						LogErr("Send ack", err)
					}
				} else {
					err = s.tryPingHost()
					if err != nil {
						err = responder.Send(zmq.NewMsg(append([]byte{2})))
					} else {
						err = responder.Send(zmq.NewMsg(append([]byte{0})))
					}
				}
			}
		}
	}()
	return nil
}

func (s *server) SetOrderHandlerPerMenu(f []func(context.Context, *Order)) {
	if s.handleCtxCancel != nil {
		s.handleCtxCancel()
		defer func() {
			go s.handleOrders()
		}()
	}
	if len(f) == 0 {
		f = make([]func(context.Context, *Order), len(s.orderListener))
	}
	for i := range s.orderListener {
		if f[i] == nil && s.orderListener[i] != nil {
			LogInfo("disable menu", s.nodeId, i)
		}
	}
	s.orderListener = f
	if s.orderServerByMenu == nil {
		s.orderServerByMenu = make([]ILoadBalancer, len(f))
		if s.nodeId != 0 {
			for i := range f {
				s.orderServerByMenu[i] = NewLoadBalancer(s.nodeId)
			}
		}
	}
	s.status.ServeMenuIds = make([]uint32, 0, len(f))
	for i := range f {
		if f[i] != nil {
			s.status.ServeMenuIds = append(s.status.ServeMenuIds, uint32(i))
		}
	}

	if s.hostCtx != nil {
		s.recvStatusCh <- nil
	} else if s.pushStatusCh != nil {
		s.pushStatusCh <- struct{}{}
	}
}

func (s *server) addNodeAndReply(statusData []byte) []byte {
	var (
		ok         bool
		n          *node
		url        string
		nodeStatus = &deliveryProto.NodeStatus{}
	)
	err := proto.Unmarshal(statusData, nodeStatus)
	if err != nil {
		LogErr("Unmarshal node status", err)
		return nil
	}
	url = fmt.Sprintf("%s:%d", nodeStatus.Host, nodeStatus.RepPort)
	s.Lock()
	if n, ok = s.peersByUrl[url]; !ok {
		nodeStatus.NodeId = uint32(len(s.peers)) + 1
		LogInfo("new node", s.nodeId, nodeStatus.NodeId, nodeStatus.Host, nodeStatus.RepPort)
		n = newNode(s, nodeStatus.Host, uint16(nodeStatus.RepPort), nodeStatus.NodeId, DefaultReqSocketMax)
		n.status = nodeStatus
		s.peers = append(s.peers, n)
		s.peersByUrl[url] = n
		s.Unlock()
		s.recvStatusCh <- nil
		go n.initRegister(0)
	} else {
		if s.peers[n.Id-1].status.Offline {
			s.peers[n.Id-1].status = nodeStatus
			s.recvStatusCh <- nil
		}
		s.Unlock()
	}

	reply := make([]byte, 5)
	reply[0] = MSG_FLAG_CONNECTED_ACK
	binary.BigEndian.PutUint32(reply[1:], n.Id)

	statusData, _ = proto.Marshal(s.genStatus())

	return append(reply, statusData...)
}

var (
	orderWrapPool = sync.Pool{
		New: func() interface{} {
			return &Order{}
		},
	}
	deliverableWrapPool = sync.Pool{
		New: func() interface{} {
			return &deliveryProto.Deliverable{}
		},
	}
)

func (s *server) handleOrders() {
	var (
		statForSeconds = make([]*uint32, 60)
		menuServing    = make([]bool, len(s.orderListener))
	)
	for i := range statForSeconds {
		statForSeconds[i] = new(uint32)
	}
	s.callStatForSecond = statForSeconds[0]
	s.handleCtx, s.handleCtxCancel = context.WithCancel(s.ctx)
	go func() {
		var (
			ticker = time.NewTicker(time.Second)
			i      int
		)
		for {
			select {
			case <-ticker.C:
				i = (i + 1) % 60
				//not really that important so don't lock
				s.status.ProcessedInMinute -= atomic.SwapUint32(statForSeconds[i], 0)
				s.status.ProcessedInMinute += atomic.LoadUint32(s.callStatForSecond)
				s.callStatForSecond = statForSeconds[i]
			case <-s.handleCtx.Done():
				ticker.Stop()
				return
			}
		}
	}()
	for i := range s.orderListener {
		if s.orderListener[i] != nil {
			menuServing[i] = true
		}
	}
	for i := 0; i < DefaultOrderHandleConcurrentLimit; i++ {
		go func() {
			var (
				order *Order
				ok    bool
			)
			for {
				select {
				case <-s.handleCtx.Done():
					return
				case order, ok = <-s.orderCh:
					if !ok {
						return
					}
					if order.Ctx.Err() != nil {
						continue
					}
					if menuServing[order.MenuId] {
						order.Ack()
						//LogErr("handle order-------------", nil, s.nodeId, order.MenuId)
						atomic.AddUint32(s.callStatForSecond, 1)
						//atomic.AddUint32(&traceRing[order.Id*10+uint64(order.NodeId)], 8)
						s.orderListener[order.MenuId](order.Ctx, order)
						//atomic.AddUint32(&traceRing[order.Id*10+uint64(order.NodeId)], 16)
						atomic.AddInt32(s.loadingPtr, -1)
						orderWrapPool.Put(order)
					} else {
						order.Response(nil, ErrMenuNotServing)
						orderWrapPool.Put(order)
					}

				}
			}
		}()
	}
}

func (s *server) asMainHost() {
	s.hostCtx, s.hostCtxCancel = context.WithCancel(s.ctx)
	go s.updateNodeStatusLoop()
}

func (s *server) genStatus() *deliveryProto.ChainStatus {
	var (
		chainStatus = &deliveryProto.ChainStatus{}
		nodeStatus  []deliveryProto.NodeStatus
		l           int
		i           int
	)
	s.RLock()
	l = len(s.peers)
	nodeStatus = make([]deliveryProto.NodeStatus, l)
	chainStatus.NodeStatus = make([]*deliveryProto.NodeStatus, l)
	for i = range s.peers {
		nodeStatus[i] = *s.peers[i].status
		chainStatus.NodeStatus[i] = &nodeStatus[i]
	}
	chainStatus.LeaderRank = s.leaderRank[:]
	s.RUnlock()
	return chainStatus
}

func (s *server) pushStatusLoop() {
	var (
		resourceTicker = time.NewTicker(DefaultStatusBroadcastInterval/2 - time.Millisecond) //make sure it fires before the first status push
		ticker         = time.NewTicker(DefaultStatusBroadcastInterval)
		err            error
		statusHeader   = []byte{MSG_FLAG_STATUS_SUBMIT}
		nodeStatus     deliveryProto.NodeStatus
		ctx            context.Context
		stat           *cpu.Stats
		beforeStat     *cpu.Stats
		total          float64
	)
	s.pushStatusCh = make(chan struct{}, 1)
	s.hostHeartbeat = time.NewTimer((DefaultStatusBroadcastInterval * time.Duration(12)) / 10)
	ctx, s.pushStatusCtxCancel = context.WithCancel(s.ctx)
	pushStatus := func() {
		nodeStatus = *s.status
		nodeStatus.SendTime = time.Now().UnixMicro()
		data, _ := proto.Marshal(&nodeStatus)
		err = s.peers[0].pushMessage(append(statusHeader, data...))
		if err != nil {
			LogErr("push status", err)
			ticker.Stop()
			s.onSendErr(1, err)
			return
		}
	}
	getResourceUsage := func() {
		stat, err = cpu.Get()
		if err == nil {
			if beforeStat != nil {
				total = float64(stat.Total - beforeStat.Total)
				s.status.CpuUsage = float64(stat.User-beforeStat.User) / total * 100
			}
			beforeStat = stat
		} else {
			if !strings.Contains(err.Error(), "not implemented") {
				LogErr("get cpu stat", err)
			}
		}
	}
	for {
		select {
		case <-resourceTicker.C:
			getResourceUsage()
		case <-s.hostHeartbeat.C:
			LogDebug("host heartbeat delay", s.nodeId)
			_ = s.tryPingHost()
		case <-s.pushStatusCh:
			getResourceUsage()
			pushStatus()
		case <-ticker.C:
			pushStatus()
		case <-ctx.Done():
			s.hostHeartbeat.Stop()
			ticker.Stop()
			return
		}
	}
}

func (s *server) updateNodeStatusLoop() {
	s.recvStatusCh = make(chan []byte, 100)
	var (
		nodeStatus       *deliveryProto.NodeStatus
		leaderRankTicker = time.NewTicker(DefaultStatusBroadcastInterval)
		broadcastHeader  = []byte{MSG_FLAG_STATUS_BOARDCASE}
		data, statusData []byte
		err              error
	)
	broadcastStatus := func() {
		LogDebug("broadcast status", s.nodeId, len(s.peers))
		chainStatus := s.genStatus()
		data, err = proto.Marshal(chainStatus)
		if err != nil {
			LogErr("Marshal status", err)
		}
		data = append(broadcastHeader, data...)
		s.RLock()
		for _, n := range s.peers[1:] {
			err = n.pushMessage(data)
			if err != nil {
				LogErr("broadcastStatus err", err)
				s.onSendErr(n.Id, err)
			}
		}
		s.RUnlock()
		s.checkNodes(chainStatus, nil)
	}
	for {
		select {
		case <-s.hostCtx.Done():
			leaderRankTicker.Stop()
			close(s.recvStatusCh)
			return
		case statusData = <-s.recvStatusCh:
			if statusData == nil {
				LogDebug("force push status", nil)
				broadcastStatus()
				continue
			}
			nodeStatus = &deliveryProto.NodeStatus{}
			err = proto.Unmarshal(statusData, nodeStatus)
			if err != nil {
				LogErr("Unmarshal status", err)
				continue
			}
			s.Lock()
			//LogErr("got push status 1", nil, len(s.peers), nodeStatus.NodeId-1)
			if len(s.peers) > int(nodeStatus.NodeId-1) {
				nodeStatus.SendTime -= time.Now().UnixMicro()
				if nodeStatus.SendTime < 0 {
					nodeStatus.SendTime = 0
				}
				if fmt.Sprintf("%v", s.peers[nodeStatus.NodeId-1].status.ServeMenuIds) != fmt.Sprintf("%v", nodeStatus.ServeMenuIds) {
					s.peers[nodeStatus.NodeId-1].status = nodeStatus
					s.Unlock()
					//LogDebug("change menu push status", nil)
					broadcastStatus()
				} else {
					s.peers[nodeStatus.NodeId-1].status = nodeStatus
					s.Unlock()
				}
				//logErr("update status", nil, s.peers[nodeId].status)
			} else {
				s.Unlock()
			}

		case <-leaderRankTicker.C:
			s.Lock()
			peers := s.peers[:]
			s.Unlock()
			sort.Slice(peers, func(i, j int) bool {
				return peers[i].calcLeaderRank() > peers[j].calcLeaderRank()
			})
			s.leaderRank = make([]uint32, len(peers))
			for i := range peers {
				if !peers[i].status.Offline {
					s.leaderRank[i] = peers[i].Id
				}
			}
			broadcastStatus()

		}

	}

}

func (s *server) checkNodes(chainStatus *deliveryProto.ChainStatus, n *node) {
	LogDebug("checkNodes", nil, s.nodeId, len(chainStatus.NodeStatus))
	var (
		nodeStatus *deliveryProto.NodeStatus
		nodeId     uint32
	)
	if s.hostHeartbeat != nil {
		s.hostHeartbeat.Reset((DefaultStatusBroadcastInterval * time.Duration(12)) / 10)
	}
	if s.peers == nil {
		s.Lock()
		s.peers = make([]*node, len(chainStatus.NodeStatus))
		defer s.Unlock()
	}
	var (
		l            = uint32(len(s.peers))
		menuHandlers [][]IHandler
		menuId       uint32
	)
	if ll := len(s.orderListener); ll > 0 {
		menuHandlers = make([][]IHandler, ll)
	}
	if l != uint32(len(chainStatus.NodeStatus)) {
		s.Lock()
		for i := l; i < uint32(len(chainStatus.NodeStatus)); i++ {
			s.peers = append(s.peers, nil)
		}
		s.Unlock()
		l = uint32(len(chainStatus.NodeStatus))
	}
	if s.nodeId != 1 {
		for _, nodeStatus = range chainStatus.NodeStatus {
			nodeId = nodeStatus.NodeId - 1
			if l <= nodeId {
				continue
			}
			if s.peers[nodeId] == nil {
				if n != nil && nodeStatus.Host == n.url && nodeStatus.RepPort == n.status.RepPort {
					s.peers[nodeId] = n
					s.peers[nodeId].Id = nodeStatus.NodeId
				} else {
					s.peers[nodeId] = newNode(s, nodeStatus.Host, uint16(nodeStatus.RepPort), nodeStatus.NodeId, DefaultReqSocketMax)
					s.peers[nodeId].Id = nodeStatus.NodeId
					if nodeStatus.NodeId != s.nodeId {
						go s.peers[nodeId].initRegister(0)
					} else {
						nodeStatus.MemoryMB = s.status.MemoryMB
						nodeStatus.CpuCore = s.status.CpuCore
						s.status = nodeStatus
					}
				}
				LogInfo("checkNodes new node", s.nodeId, nodeStatus.NodeId)
			}
			if s.peers[nodeId].
				status.
				FailCount > nodeStatus.
				FailCount {
				nodeStatus.FailCount = s.peers[nodeId].status.FailCount
			}
			if nodeStatus.NodeId != s.nodeId {
				s.peers[nodeId].status = nodeStatus
			}
			if len(menuHandlers) < len(nodeStatus.ServeMenuIds) {
				menuHandlers = append(menuHandlers, nil)
			}
			LogDebug("checkNodes ServeMenuIds", s.nodeId, nodeStatus.NodeId, nodeStatus.ServeMenuIds)
			for _, menuId = range nodeStatus.ServeMenuIds {
				menuHandlers[menuId] = append(menuHandlers[menuId], s.peers[nodeId])
			}
		}
		if len(chainStatus.LeaderRank) >= len(s.leaderRank) {
			s.leaderRank = chainStatus.LeaderRank[:]
		}
	} else {
		for _, nodeStatus = range chainStatus.NodeStatus {
			nodeId = nodeStatus.NodeId - 1
			//LogErr("------------------------------", nil, s.nodeId, nodeStatus.ServeMenuIds, nodeStatus.NodeId)
			if !nodeStatus.Offline && len(s.orderListener) >= len(nodeStatus.ServeMenuIds) {
				for _, menuId = range nodeStatus.ServeMenuIds {
					menuHandlers[menuId] = append(menuHandlers[menuId], s.peers[nodeId])
				}
			}
		}
	}
	if s.orderServerByMenu == nil {
		s.orderServerByMenu = make([]ILoadBalancer, len(menuHandlers))
	}
	if len(menuHandlers) == 0 {
		for _, handler := range s.orderServerByMenu {
			handler.UpdateHandlers(nil)
		}
	} else {

		for menuId, handlers := range menuHandlers {
			if s.orderServerByMenu[menuId] == nil && s.nodeId != 0 {
				s.orderServerByMenu[menuId] = NewLoadBalancer(s.nodeId)
			}
			s.orderServerByMenu[menuId].UpdateHandlers(handlers)
		}
	}
	//LogErr("checkNodes", nil, s.nodeId, s.orderServerByMenu)
}

func (s *server) onSendErr(nodeId uint32, err error) {
	if s.ctx.Err() == nil {
		if isConnLost(err) {
			if nodeId == 1 {
				s.Lock()
				peers := make([]*node, 0, len(s.peers)-1)
				peersByUrl := make(map[string]*node, len(s.peers)-1)
				LogDebug("find new host", s.leaderRank)
				for i, n := range s.leaderRank[1:] {
					if n == s.nodeId {
						LogInfo("switch to main host", i, s.nodeId, n)
						s.pushStatusCtxCancel()
						s.asMainHost()
						s.status = s.peers[n-1].status
						s.nodeId = 1
						peers = append(peers, s.peers[n-1])
						peersByUrl[fmt.Sprintf("%s:%d", peers[len(peers)-1].url, peers[len(peers)-1].status.RepPort)] = peers[len(peers)-1]
						break
					} else {
						data, err := s.peers[n-1].pushMessageAndRecv([]byte{MSG_FLAG_STATUS_INHERIT})
						if err != nil {
							LogErr("inherit status", err)
							continue
						}
						switch data[0] {
						case 2:
							time.Sleep(time.Millisecond)
						case 0:
							s.Unlock()
							return
						}
						data = s.peers[n-1].connectToHost(0)
						if len(data) < 5 || data[0] != MSG_FLAG_CONNECTED_ACK {
							LogErr("node invalid ack", nil, data)
							continue
						}
						s.nodeId = binary.BigEndian.Uint32(data[1:5])
						chainStatus := &deliveryProto.ChainStatus{}
						err = proto.Unmarshal(data[5:], chainStatus)
						if err != nil {
							LogErr("node initRegister Unmarshal", err)
							return
						}
						s.checkNodes(chainStatus, s.peers[n-1])
						peers = append(peers, s.peers[n-1])
						peersByUrl[fmt.Sprintf("%s:%d", peers[len(peers)-1].url, peers[len(peers)-1].status.RepPort)] = peers[len(peers)-1]
						break
					}
				}
				if len(peers) != 0 {
					for _, n := range s.peers[1:] {
						if n != peers[0] {
							data, err := n.pushMessageAndRecv([]byte{MSG_FLAG_STATUS_PING})
							if err != nil {
								n.server.onSendErr(n.Id, err)
								continue
							} else if data[0] != MSG_FLAG_STATUS_PING {
								LogErr("ping invalid ack", nil, data)
								continue
							}
							peers = append(peers, n)
							peersByUrl[fmt.Sprintf("%s:%d", n.url, n.status.RepPort)] = n
						}
					}
					s.peers = peers
					s.peersByUrl = peersByUrl
					s.leaderRank = []uint32{1}
					s.Unlock()
				} else {
					s.Unlock()
					time.Sleep(time.Second)
					s.onSendErr(nodeId, err)
				}
			} else {
				s.peers[nodeId-1].status.Offline = true
				s.recvStatusCh <- nil
			}
		} else {
			s.Lock()
			atomic.AddUint32(&s.peers[nodeId-1].status.FailCount, 1)
			s.Unlock()
		}
	}
}

func (s *server) SwitchLeader(url string, port uint16) {
	if s.hostCtx != nil {
		s.hostCtxCancel()
	}
	s.hostRepPort = port
	s.SwitchHost(url)
}

func (s *server) tryPingHost() (err error) {
	_, err = s.peers[0].pushMessageAndRecv([]byte{MSG_FLAG_STATUS_PING})
	if err != nil {
		LogErr("host heartbeat err", err)
		s.onSendErr(1, err)
	} else {
		LogDebug("host heartbeat OK")
	}
	return
}

var (
	ErrRunInLocal = errors.New("run in local")
)

func (s *server) Order(menuId, dishId uint16, skipNodeId ...uint32) (func(ctx context.Context, input []byte) ([]byte, error), error) {
	nodeId := s.orderServerByMenu[menuId].GetNodeId(skipNodeId...)
	//LogErr("Order!!!!!!!!!!!!!!", nil, s.nodeId, menuId, nodeId)
	if nodeId == s.nodeId-1 {
		atomic.AddUint32(s.callStatForSecond, 1)
		return nil, ErrRunInLocal
	} else if nodeId == math.MaxUint32 {
		return nil, ErrMenuNotServing
	}
	return func(ctx context.Context, input []byte) ([]byte, error) {
		output, err := s.peers[nodeId].OrderRequestAsClient(ctx, menuId, dishId, input)
		if errors.Is(err, ErrMenuNotServing) {
			fn, err := s.Order(menuId, dishId, append(skipNodeId, nodeId)...)
			if err != nil {
				return nil, err
			}
			return fn(ctx, input)
		}
		return output, err
	}, nil
}

func isConnLost(err error) bool {
	return errors.Is(err, zmq.ErrClosedConn) || strings.Contains(err.Error(), "dial") || strings.Contains(err.Error(), "no connections")
}
