package delivery

import (
	"context"
	"github.com/stretchr/testify/assert"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

var (
	mainServers = make([]ILogistic, 2)
	nodeServers = make([]ILogistic, 3)
	count       []int
)

func init() {
	var (
		err error
		l   = len(mainServers)
	)
	DefaultStatusBroadcastInterval = time.Second * 10
	count = make([]int, l)
	for i := 0; i < l; i++ {
		mainServers[i] = NewServer("tcp://127.0.0.1", uint16(10000+i), "", 0)

		mainServers[i].SetOrderHandlerPerMenu((func(i int) []func(context.Context, *Order) {
			return []func(ctx context.Context, order *Order){func(ctx context.Context, order *Order) {
				//time.Sleep(time.Millisecond * 50)
				count[i]++
				order.Response(order.Input, nil)
			}}
		})(i))
		err = mainServers[i].Init(context.Background())
		if err != nil {
			panic(err)
		}
	}
	time.Sleep(time.Millisecond * 100)
	for i, l := 0, len(nodeServers); i < l; i++ {
		nodeServers[i] = NewServer("tcp://127.0.0.1", uint16(20000+i), "tcp://127.0.0.1", uint16(10000+i%len(mainServers)))
		err = nodeServers[i].Init(context.Background())
		if err != nil {
			panic(err)
		}
	}
	time.Sleep(time.Millisecond * 1000)
}

func TestOrder(t *testing.T) {
	resp, err := nodeServers[0].(*server).peers[0].OrderRequestAsClient(context.Background(), 0, 0, []byte("hello"))
	assert.Nil(t, err)
	assert.Equal(t, []byte("hello"), resp)
}

func BenchmarkOrder(b *testing.B) {
	ii := int32(0)
	b.SetParallelism(1000)
	b.RunParallel(func(pb *testing.PB) {
		var (
			err    error
			s            = atomic.AddInt32(&ii, 1) % int32(len(nodeServers))
			i      int64 = 0
			data   []byte
			res    []byte
			ctx    = context.Background()
			server = nodeServers[s].(*server)
		)
		for pb.Next() {
			data = []byte("hello" + strconv.FormatInt(i, 10))
			res, err = server.peers[0].OrderRequestAsClient(ctx, 0, 0, data)
			if err != nil {
				b.Fatal(err)
			}
			if string(data) != string(res) {
				b.Fatalf("expected %s, got %s", string(data), string(res))
			}
			i++
		}
	})
}

func TestDisable(t *testing.T) {
	cnt := count[0]
	mainServers[0].SetOrderHandlerPerMenu(nil)
	resp, err := nodeServers[0].(*server).peers[0].OrderRequestAsClient(context.Background(), 0, 0, []byte("hello"))
	assert.Equal(t, ErrMenuNotServing, err)
	assert.Nil(t, resp)
	time.Sleep(time.Millisecond * 100)
	fn, err := nodeServers[0].Order(0, 0)
	assert.Equal(t, ErrRunInLocal, err)
	assert.Nil(t, fn)
	nodeServers[0].SetOrderHandlerPerMenu([]func(context.Context, *Order){func(ctx context.Context, order *Order) {
		order.Response(order.Input, nil)
	}})
	time.Sleep(time.Millisecond * 100)
	for i, l := 0, len(mainServers)*5; i < l; i++ {
		fn, err := mainServers[0].Order(0, 0)
		assert.Nil(t, err)
		resp, err := fn(context.Background(), []byte("hello"))
		assert.Nil(t, err)
		assert.Equal(t, []byte("hello"), resp)
	}
	assert.Equal(t, cnt, count[0])
}

func TestShutdown(t *testing.T) {
	leader := 0
	mainServers[0].Shutdown()
	assert.False(t, mainServers[0].IsLeader())
	time.Sleep(time.Second * 1)
	for i, l := 0, len(mainServers); i < l; i++ {
		if mainServers[i].IsLeader() {
			leader++
		}
	}
	for i, l := 0, len(nodeServers); i < l; i++ {
		if nodeServers[i].IsLeader() {
			leader++
		}
	}
	assert.Equal(t, 1, leader)
}
